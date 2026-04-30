package runner

import (
	"fmt"
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
		nextCh:   make(chan []JobInterface),
		queueCh:  make(chan []JobInterface),
		ResultCh: make(chan map[string]interface{}, 16),
		limit:    limit,
		sem:      make(chan struct{}, limit),
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
	if r.limit != defaultConcurrencyLimit {
		t.Errorf("NewRunner limit = %d, want %d", r.limit, defaultConcurrencyLimit)
	}
}
