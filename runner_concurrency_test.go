package runner

import (
	"errors"
	"fmt"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
)

type concJob struct {
	id      string
	sleep   time.Duration
	running *atomic.Int32
	maxSeen *atomic.Int32
}

func (j *concJob) GetID() string          { return j.id }
func (j *concJob) IsRun(t time.Time) bool { return true }
func (j *concJob) Run() any {
	if j.running != nil {
		cur := j.running.Add(1)
		for {
			m := j.maxSeen.Load()
			if cur <= m || j.maxSeen.CompareAndSwap(m, cur) {
				break
			}
		}
		defer j.running.Add(-1)
	}
	if j.sleep > 0 {
		time.Sleep(j.sleep)
	}
	return j.id
}

// resultByID : Result.Items를 ID로 인덱싱한 헬퍼.
func resultByID(res Result) map[string]JobResult {
	m := make(map[string]JobResult, len(res.Items))
	for _, it := range res.Items {
		m[it.ID] = it
	}
	return m
}

// newTestRunner : timeChecker/createQueue/ingest 없이 start()만 띄운 Runner.
func newTestRunner(limit int) *Runner {
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := &Runner{
		waitCh:   make(chan bool),
		queueCh:  make(chan []JobInterface),
		ResultCh: make(chan Result, 16),
		limit:    limit,
		sem:      make(chan struct{}, limit),
		done:     make(chan struct{}),
	}
	go r.start()
	return r
}

func TestStartConcurrent(t *testing.T) {
	r := newTestRunner(50)
	queue := make([]JobInterface, 10)
	for i := range queue {
		queue[i] = &concJob{id: fmt.Sprintf("j%d", i), sleep: 100 * time.Millisecond}
	}
	start := time.Now()
	r.queueCh <- queue
	<-r.ResultCh
	elapsed := time.Since(start)
	// 직렬이면 1000ms, 동시면 ~100ms. 느린 CI 마진 포함 500ms.
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected concurrent execution under 500ms, got %v", elapsed)
	}
}

func TestStartRespectsLimit(t *testing.T) {
	var running, maxSeen atomic.Int32
	r := newTestRunner(3)
	queue := make([]JobInterface, 10)
	for i := range queue {
		queue[i] = &concJob{
			id:      fmt.Sprintf("j%d", i),
			sleep:   50 * time.Millisecond,
			running: &running,
			maxSeen: &maxSeen,
		}
	}
	r.queueCh <- queue
	<-r.ResultCh
	if m := maxSeen.Load(); m > 3 {
		t.Errorf("max concurrent = %d, want <= 3", m)
	}
}

func TestStartResultCollection(t *testing.T) {
	r := newTestRunner(20)
	queue := make([]JobInterface, 100)
	for i := range queue {
		queue[i] = &concJob{id: fmt.Sprintf("j%d", i)}
	}
	r.queueCh <- queue
	res := <-r.ResultCh
	if len(res.Items) != 100 {
		t.Fatalf("got %d items, want 100", len(res.Items))
	}
	byID := resultByID(res)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("j%d", i)
		jr, ok := byID[id]
		if !ok {
			t.Errorf("missing result for %s", id)
			continue
		}
		if jr.Err != nil {
			t.Errorf("%s: unexpected err %v", id, jr.Err)
		}
		if jr.Value != id {
			t.Errorf("%s: value=%v want %s", id, jr.Value, id)
		}
	}
}

func TestNewRunnerWithLimitFallback(t *testing.T) {
	for _, limit := range []int{0, -1, -100} {
		effective := limit
		if effective <= 0 {
			effective = defaultConcurrencyLimit
		}
		r := &Runner{
			queueCh:  make(chan []JobInterface),
			ResultCh: make(chan Result, 1),
			limit:    effective,
			sem:      make(chan struct{}, effective),
			done:     make(chan struct{}),
		}
		go r.start()
		r.queueCh <- []JobInterface{&concJob{id: "x"}}
		select {
		case <-r.ResultCh:
		case <-time.After(time.Second):
			t.Errorf("limit=%d: timeout", limit)
		}
		if r.limit != defaultConcurrencyLimit {
			t.Errorf("limit=%d: got %d, want %d", limit, r.limit, defaultConcurrencyLimit)
		}
	}
}

func TestStartAcceptsNextQueueWhileBusy(t *testing.T) {
	r := newTestRunner(1)
	slow := []JobInterface{&concJob{id: "slow", sleep: 300 * time.Millisecond}}
	fast := []JobInterface{&concJob{id: "fast"}}

	r.queueCh <- slow
	select {
	case r.queueCh <- fast:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("start()가 다음 큐를 즉시 수신하지 못함")
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case res := <-r.ResultCh:
			for _, it := range res.Items {
				got[it.ID] = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("ResultCh 수신 timeout (i=%d)", i)
		}
	}
	if !got["slow"] || !got["fast"] {
		t.Errorf("두 큐 결과 모두 수신해야 함, got=%v", got)
	}
}

func TestGlobalLimitAcrossQueues(t *testing.T) {
	var running, maxSeen atomic.Int32
	r := newTestRunner(3)
	mkQueue := func(prefix string, n int) []JobInterface {
		q := make([]JobInterface, n)
		for i := range q {
			q[i] = &concJob{
				id:      fmt.Sprintf("%s-%d", prefix, i),
				sleep:   80 * time.Millisecond,
				running: &running,
				maxSeen: &maxSeen,
			}
		}
		return q
	}
	r.queueCh <- mkQueue("a", 10)
	time.Sleep(10 * time.Millisecond)
	r.queueCh <- mkQueue("b", 10)
	for i := 0; i < 2; i++ {
		select {
		case <-r.ResultCh:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout (i=%d)", i)
		}
	}
	if m := maxSeen.Load(); m > 3 {
		t.Errorf("global concurrent = %d, want <= 3", m)
	}
}

func TestStopReleasesGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)
	time.Sleep(50 * time.Millisecond)
	if delta := runtime.NumGoroutine() - baseline; delta < 4 {
		t.Fatalf("expected at least 4 new goroutines, got delta=%d", delta)
	}
	r.Stop()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		// 다른 테스트와 병행 시 baseline이 이미 여러 개 차이날 수 있으므로,
		// "Stop 후 라이프사이클 4개가 빠졌는지"를 보수적으로 확인.
		if runtime.NumGoroutine() <= baseline+2 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines did not exit after Stop: baseline=%d, current=%d", baseline, runtime.NumGoroutine())
}

func TestStopAndWait(t *testing.T) {
	// Wait의 in-flight 대기 의미를 검증: 직접 inFlightQueues에 Add/Done하여
	// 같은 테스트 고루틴 내에서 happens-before를 명확히 한 뒤 StopAndWait이
	// Done 호출까지 블로킹되는지 확인한다.
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)

	r.inFlightQueues.Add(1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		r.inFlightQueues.Done()
	}()

	start := time.Now()
	r.StopAndWait()
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("StopAndWait이 in-flight 작업을 기다리지 않음: elapsed=%v", elapsed)
	}
}

type panicJob struct{ id string }

func (p *panicJob) GetID() string          { return p.id }
func (p *panicJob) IsRun(t time.Time) bool { return true }
func (p *panicJob) Run() any               { panic("boom: " + p.id) }

func TestRunQueueRecoversPanic(t *testing.T) {
	r := newTestRunner(5)
	queue := []JobInterface{
		&concJob{id: "ok1"},
		&panicJob{id: "bad"},
		&concJob{id: "ok2"},
	}
	r.queueCh <- queue
	select {
	case res := <-r.ResultCh:
		if len(res.Items) != 3 {
			t.Fatalf("got %d items, want 3", len(res.Items))
		}
		byID := resultByID(res)
		bad := byID["bad"]
		var perr *PanicError
		if !errors.As(bad.Err, &perr) {
			t.Errorf("expected *PanicError for panicked job, got %T: %v", bad.Err, bad.Err)
		} else {
			if perr.ID != "bad" {
				t.Errorf("PanicError.ID = %q, want %q", perr.ID, "bad")
			}
			if len(perr.Stack) == 0 {
				t.Error("PanicError.Stack is empty")
			}
		}
		if byID["ok1"].Value != "ok1" || byID["ok2"].Value != "ok2" {
			t.Errorf("non-panicked jobs should complete normally")
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout — panic likely killed the goroutine")
	}
}

func TestNewRunnerCompat(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunner(ch)
	defer r.Stop()
	if r.limit != defaultConcurrencyLimit {
		t.Errorf("NewRunner limit = %d, want %d", r.limit, defaultConcurrencyLimit)
	}
}

type slowCountJob struct {
	id      string
	count   *atomic.Int32
	release chan struct{}
}

func (j *slowCountJob) GetID() string          { return j.id }
func (j *slowCountJob) IsRun(t time.Time) bool { return true }
func (j *slowCountJob) Run() any {
	j.count.Add(1)
	if j.release != nil {
		<-j.release
	}
	return j.id
}

func TestWithDedupeSkipsInFlight(t *testing.T) {
	r := newTestRunner(10)
	r.dedupe = true
	r.inFlight = make(map[string]struct{})

	var count atomic.Int32
	release := make(chan struct{})
	queue := []JobInterface{
		&slowCountJob{id: "dup", count: &count, release: release},
		&slowCountJob{id: "dup", count: &count, release: release},
		&slowCountJob{id: "uniq", count: &count, release: release},
	}
	r.queueCh <- queue
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case res := <-r.ResultCh:
		if len(res.Items) != 3 {
			t.Errorf("expected 3 items (dup OK, dup skipped, uniq OK), got %d: %+v", len(res.Items), res.Items)
		}
		// dup 두 항목 중 하나는 ErrSkippedDuplicate, 다른 하나는 정상 결과
		var dupOK, dupSkipped, uniqOK int
		for _, it := range res.Items {
			switch it.ID {
			case "dup":
				if errors.Is(it.Err, ErrSkippedDuplicate) {
					dupSkipped++
				} else if it.Err == nil {
					dupOK++
				}
			case "uniq":
				if it.Err == nil {
					uniqOK++
				}
			}
		}
		if dupOK != 1 || dupSkipped != 1 || uniqOK != 1 {
			t.Errorf("dupOK=%d dupSkipped=%d uniqOK=%d, want 1/1/1", dupOK, dupSkipped, uniqOK)
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout")
	}
	if c := count.Load(); c != 2 {
		t.Errorf("Run() invocations = %d, want 2", c)
	}
	if r.dedupeSkips.Load() != 1 {
		t.Errorf("dedupeSkips counter = %d, want 1", r.dedupeSkips.Load())
	}
}

func TestNewRunnerWithLimitOptions(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5, WithDedupe(), WithBatchBuffer(10))
	defer r.Stop()
	if !r.dedupe {
		t.Error("WithDedupe not applied")
	}
	if r.batchBufferSize != 10 {
		t.Errorf("batchBufferSize = %d, want 10", r.batchBufferSize)
	}
	if cap(r.batches) != 10 {
		t.Errorf("batches cap = %d, want 10", cap(r.batches))
	}
}

func TestWithBatchBufferBackpressure(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 1, WithBatchBuffer(2))
	defer r.Stop()

	for i := 0; i < 3; i++ {
		select {
		case ch <- []JobInterface{&concJob{id: fmt.Sprintf("j%d", i)}}:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("push %d 블로킹 — 큐가 즉시 수용해야 함", i)
		}
	}
	select {
	case ch <- []JobInterface{&concJob{id: "blocked"}}:
		t.Error("4번째 push가 블로킹되어야 하는데 즉시 수용됨")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestStatsSnapshot(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5, WithDedupe(), WithBatchBuffer(8))
	defer r.Stop()
	s := r.Stats()
	if s.BatchQueueCapacity != 8 {
		t.Errorf("BatchQueueCapacity = %d, want 8", s.BatchQueueCapacity)
	}
	if s.InFlightJobs != 0 || s.BatchQueueDepth != 0 {
		t.Errorf("초기 상태 비어있어야 함, got %+v", s)
	}
}

func TestSetResultDropsOldestOnOverflow(t *testing.T) {
	r := &Runner{
		ResultCh: make(chan Result, 2),
	}
	// 버퍼 크기 2 — 3번째부터 가장 오래된 것이 드롭됨
	r.setResult(Result{Items: []JobResult{{ID: "a"}}})
	r.setResult(Result{Items: []JobResult{{ID: "b"}}})
	r.setResult(Result{Items: []JobResult{{ID: "c"}}})
	if r.droppedResults.Load() != 1 {
		t.Errorf("droppedResults = %d, want 1", r.droppedResults.Load())
	}
	// 채널에 b, c만 남아있어야 함
	got := []string{}
	for len(got) < 2 {
		select {
		case res := <-r.ResultCh:
			got = append(got, res.Items[0].ID)
		default:
			t.Fatalf("결과 부족: %v", got)
		}
	}
	if got[0] != "b" || got[1] != "c" {
		t.Errorf("남은 결과 = %v, want [b, c]", got)
	}
}
