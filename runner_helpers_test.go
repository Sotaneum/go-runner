// SPDX-License-Identifier: MIT

package runner

import (
	"sync/atomic"
	"testing"
	"time"
)

// concJob : 동시성/세마포어 검증용 Job. running/maxSeen이 주입되면 동시 실행 카운트를 추적한다.
type concJob struct {
	id      string
	sleep   time.Duration
	running *atomic.Int32
	maxSeen *atomic.Int32
}

func (j *concJob) GetID() string          { return j.id }
func (j *concJob) IsRun(t time.Time) bool { return true }
func (j *concJob) Run() (any, error) {
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
	return j.id, nil
}

// panicJob : Run에서 panic을 던지는 Job. recover/PanicError 검증용.
type panicJob struct{ id string }

func (p *panicJob) GetID() string          { return p.id }
func (p *panicJob) IsRun(t time.Time) bool { return true }
func (p *panicJob) Run() (any, error)      { panic("boom: " + p.id) }

// errJob : Run에서 에러를 반환하는 Job. JobResult.Err 전파 검증용.
type errJob struct {
	id  string
	err error
}

func (e *errJob) GetID() string          { return e.id }
func (e *errJob) IsRun(t time.Time) bool { return true }
func (e *errJob) Run() (any, error)      { return nil, e.err }

// slowCountJob : release 채널이 닫힐 때까지 Run을 블로킹하는 Job. dedupe in-flight 검증용.
type slowCountJob struct {
	id      string
	count   *atomic.Int32
	release chan struct{}
}

func (j *slowCountJob) GetID() string          { return j.id }
func (j *slowCountJob) IsRun(t time.Time) bool { return true }
func (j *slowCountJob) Run() (any, error) {
	j.count.Add(1)
	if j.release != nil {
		<-j.release
	}
	return j.id, nil
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
// t.Cleanup으로 StopAndWait을 자동 등록하여 고루틴 누수를 방지한다.
func newTestRunner(t *testing.T, limit int) *Runner {
	t.Helper()
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := &Runner{
		waitCh:   make(chan bool),
		queueCh:  make(chan batchEnvelope),
		ResultCh: make(chan Result, 16),
		limit:    limit,
		sem:      make(chan struct{}, limit),
		done:     make(chan struct{}),
	}
	r.lifecycleWg.Add(1)
	go func() { defer r.lifecycleWg.Done(); r.start() }()
	t.Cleanup(r.StopAndWait)
	return r
}
