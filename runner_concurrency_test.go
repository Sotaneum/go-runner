package runner

import (
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

func (j *concJob) GetID() string         { return j.id }
func (j *concJob) IsRun(t time.Time) bool { return true }
func (j *concJob) Run() interface{} {
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

// newTestRunner : timeChecker/dispatchRunner 없이 start()만 띄운 Runner.
func newTestRunner(limit int) *Runner {
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := &Runner{
		waitCh:   make(chan bool),
		queueCh:  make(chan []JobInterface),
		ResultCh: make(chan map[string]interface{}, 16),
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
	if elapsed > 200*time.Millisecond {
		t.Errorf("expected concurrent execution under 200ms, got %v", elapsed)
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
	result := <-r.ResultCh
	if len(result) != 100 {
		t.Fatalf("got %d results, want 100", len(result))
	}
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("j%d", i)
		if result[id] != id {
			t.Errorf("result[%s] = %v, want %s", id, result[id], id)
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
			ResultCh: make(chan map[string]interface{}, 1),
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
	// limit 1로 시작해도 start()는 큐 수신을 막지 않아야 한다.
	r := newTestRunner(1)
	slow := []JobInterface{&concJob{id: "slow", sleep: 300 * time.Millisecond}}
	fast := []JobInterface{&concJob{id: "fast"}}

	r.queueCh <- slow
	// slow가 아직 도는 동안 다음 큐 수신이 즉시 가능해야 함
	select {
	case r.queueCh <- fast:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("start()가 다음 큐를 즉시 수신하지 못함")
	}
	// fast도 limit=1에 걸려 slow가 끝난 뒤 실행된다.
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case res := <-r.ResultCh:
			for id := range res {
				got[id] = true
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
	// 큐가 겹쳐 실행되더라도 전역 limit이 지켜져야 한다.
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
	// 4개 라이프사이클 고루틴 (start, createQueue, dispatchRunner, timeChecker) 시작 대기
	time.Sleep(50 * time.Millisecond)
	if runtime.NumGoroutine() < baseline+4 {
		t.Fatalf("expected at least 4 new goroutines, got delta=%d", runtime.NumGoroutine()-baseline)
	}
	r.Stop()
	// 종료 대기 — dispatchRunner가 1초 sleep 중일 수 있어 여유 있게.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= baseline+1 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("goroutines did not exit after Stop: baseline=%d, current=%d", baseline, runtime.NumGoroutine())
}

type panicJob struct{ id string }

func (p *panicJob) GetID() string         { return p.id }
func (p *panicJob) IsRun(t time.Time) bool { return true }
func (p *panicJob) Run() interface{}      { panic("boom: " + p.id) }

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
		if len(res) != 3 {
			t.Fatalf("got %d results, want 3", len(res))
		}
		if _, ok := res["bad"].(error); !ok {
			t.Errorf("expected error for panicked job, got %T: %v", res["bad"], res["bad"])
		}
		if res["ok1"] != "ok1" || res["ok2"] != "ok2" {
			t.Errorf("non-panicked jobs should complete normally, got %v / %v", res["ok1"], res["ok2"])
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout — panic likely killed the goroutine")
	}
}

func TestNewRunnerCompat(t *testing.T) {
	// 컴파일 가능 + 패닉 없이 생성되는지 확인
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

func (j *slowCountJob) GetID() string         { return j.id }
func (j *slowCountJob) IsRun(t time.Time) bool { return true }
func (j *slowCountJob) Run() interface{} {
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
		&slowCountJob{id: "dup", count: &count, release: release}, // 중복 — 스킵되어야 함
		&slowCountJob{id: "uniq", count: &count, release: release},
	}
	r.queueCh <- queue
	// 두 Job(dup 1번 + uniq)이 release 대기 중인지 확인
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case res := <-r.ResultCh:
		if len(res) != 2 {
			t.Errorf("expected 2 results (dup + uniq, 2nd dup skipped), got %d: %v", len(res), res)
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout")
	}
	if c := count.Load(); c != 2 {
		t.Errorf("Run() invocations = %d, want 2", c)
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

	// 내부 큐 용량 2 + ingest 고루틴이 1개 임시로 들고 있을 수 있어 총 3개까지 즉시 수용 가능
	for i := 0; i < 3; i++ {
		select {
		case ch <- []JobInterface{&concJob{id: fmt.Sprintf("j%d", i)}}:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("push %d 블로킹 — 큐가 즉시 수용해야 함", i)
		}
	}
	// 4번째는 큐가 가득 + ingest도 막혀서 블로킹되어야 함 (다음 분 tick 전까지 빠지지 않음)
	select {
	case ch <- []JobInterface{&concJob{id: "blocked"}}:
		t.Error("4번째 push가 블로킹되어야 하는데 즉시 수용됨")
	case <-time.After(200 * time.Millisecond):
		// 기대된 블로킹
	}
}
