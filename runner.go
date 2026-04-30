// SPDX-License-Identifier: MIT

package runner

import (
	"sync"
	"sync/atomic"

	ktime "github.com/Sotaneum/go-kst-time"
)

// Runner schedules and concurrently executes JobInterface instances every
// minute. Construct via NewRunner / NewRunnerWithLimit / New.
type Runner struct {
	waitCh   chan bool
	queueCh  chan batchEnvelope
	batches  chan batchEnvelope
	ResultCh chan Result
	limit    int

	// runnerCh : 외부 호출자가 push하는 채널. ingest가 소비한다.
	runnerCh chan []JobInterface

	// sem : 모든 큐가 공유하는 동시성 상한 세마포어.
	sem chan struct{}
	// done : Stop()으로 닫히는 종료 신호 채널.
	done chan struct{}
	// stopOnce : Stop()이 여러 번 호출되어도 done이 한 번만 close되도록 보호.
	stopOnce sync.Once
	// stoppedMu : PushAndAwait이 진행 중인 동안 Stop이 진입하지 못하도록 직렬화.
	stoppedMu sync.RWMutex
	stopped   bool
	// closeResultOnce : ResultCh를 한 번만 닫도록 보호.
	closeResultOnce sync.Once
	// lifecycleWg : start, createQueue, ingest, timeChecker 추적.
	lifecycleWg sync.WaitGroup
	// inFlightQueues : 실행 중인 runQueue 고루틴 추적. Wait()가 사용.
	inFlightQueues sync.WaitGroup

	// 옵션 필드
	batchBufferSize int
	dedupe          bool
	onPanic         func(id string, recovered any, stack []byte)
	onSkip          func(id string)

	// dedupe용 in-flight 추적
	inFlight   map[string]struct{}
	inFlightMu sync.Mutex

	// 카운터
	dedupeSkips    atomic.Uint64
	droppedResults atomic.Uint64
}

// Stop terminates the runner's lifecycle goroutines (start, createQueue,
// ingest, timeChecker) and waits for them to exit. Already-started runQueue
// and Run() goroutines continue to completion. Idempotent.
func (r *Runner) Stop() {
	r.stopOnce.Do(func() {
		// stopped를 먼저 셋하여, 진행 중인 PushAndAwait이 끝날 때까지 대기 후
		// 이후 호출은 즉시 거부되도록 한다.
		r.stoppedMu.Lock()
		r.stopped = true
		r.stoppedMu.Unlock()
		close(r.done)
		r.lifecycleWg.Wait()
	})
}

// Wait blocks until in-flight runQueues complete and then closes ResultCh,
// allowing `range r.ResultCh` to terminate naturally.
func (r *Runner) Wait() {
	r.inFlightQueues.Wait()
	r.closeResultOnce.Do(func() { close(r.ResultCh) })
}

// StopAndWait is the canonical graceful-shutdown call: Stop followed by Wait.
// After it returns, no goroutine spawned by this runner remains.
func (r *Runner) StopAndWait() {
	r.Stop()
	r.Wait()
}

// Stats returns a snapshot of operational counters.
func (r *Runner) Stats() Stats {
	return Stats{
		InFlightJobs:        len(r.sem),
		BatchQueueDepth:     len(r.batches),
		BatchQueueCapacity:  cap(r.batches),
		DedupeSkipsTotal:    r.dedupeSkips.Load(),
		DroppedResultsTotal: r.droppedResults.Load(),
	}
}

// InFlightIDs returns the IDs of jobs currently executing. Only meaningful
// when WithDedupe is enabled; otherwise returns nil.
func (r *Runner) InFlightIDs() []string {
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	if len(r.inFlight) == 0 {
		return nil
	}
	ids := make([]string, 0, len(r.inFlight))
	for id := range r.inFlight {
		ids = append(ids, id)
	}
	return ids
}

// Push sends a batch to the runner without requiring the caller to manage
// the runnerCh channel directly. Returns true if the batch was accepted,
// false if Stop has been called (in which case the batch is discarded).
// Blocks if the internal batch FIFO is full (backpressure) until either a
// slot frees up or Stop is called.
func (r *Runner) Push(batch []JobInterface) bool {
	select {
	case r.runnerCh <- batch:
		return true
	case <-r.done:
		return false
	}
}

// PushAndAwait runs a batch immediately (without waiting for the next minute
// tick), evaluating IsRun(now) once for filtering, and returns a 1-buffered
// channel that receives the Result. The same Result is also delivered on
// ResultCh. The reply channel is closed after delivery.
//
// Use this when the caller wants synchronous execution of a specific batch
// and needs to correlate its outcome — distinct from Push, which queues the
// batch for the next tick.
//
// If Stop has been called, the returned channel is closed without a result.
func (r *Runner) PushAndAwait(batch []JobInterface) <-chan Result {
	reply := make(chan Result, 1)
	r.stoppedMu.RLock()
	stopped := r.stopped
	r.stoppedMu.RUnlock()
	if stopped {
		close(reply)
		return reply
	}
	now := ktime.GetNow()
	filtered := make([]JobInterface, 0, len(batch))
	for _, j := range batch {
		if j.IsRun(now) {
			filtered = append(filtered, j)
		}
	}
	// done과 함께 select하여 Stop이 동시에 호출되어도 영구 블록되지 않도록 한다.
	// RLock은 송신 전에 풀어야 한다 — 잡고 있으면 Stop의 Lock이 막혀 done이 닫히지 않는 데드락이 된다.
	select {
	case r.queueCh <- batchEnvelope{jobs: filtered, replyCh: reply}:
	case <-r.done:
		close(reply)
	}
	return reply
}

// NewRunner creates a Runner with the default concurrency limit (50).
// runnerCh must not be nil.
func NewRunner(runnerCh chan []JobInterface) *Runner {
	return NewRunnerWithLimit(runnerCh, defaultConcurrencyLimit)
}

// NewRunnerWithLimit creates a Runner with a custom concurrency limit and
// optional configuration. runnerCh must not be nil; limit <= 0 is normalized
// to the default. Panics if runnerCh is nil.
func NewRunnerWithLimit(runnerCh chan []JobInterface, limit int, opts ...Option) *Runner {
	if runnerCh == nil {
		panic("runner: runnerCh must not be nil")
	}
	return newRunner(runnerCh, limit, opts...)
}

// New creates a Runner with an internally-managed batch channel. Use Push or
// PushAndAwait to submit batches.
func New(limit int, opts ...Option) *Runner {
	return newRunner(make(chan []JobInterface), limit, opts...)
}

func newRunner(runnerCh chan []JobInterface, limit int, opts ...Option) *Runner {
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := &Runner{
		limit:           limit,
		batchBufferSize: defaultBatchBuffer,
		runnerCh:        runnerCh,
	}
	for _, o := range opts {
		o(r)
	}

	r.waitCh = make(chan bool)
	r.queueCh = make(chan batchEnvelope)
	r.batches = make(chan batchEnvelope, r.batchBufferSize)
	r.ResultCh = make(chan Result, resultChBuffer)
	r.sem = make(chan struct{}, limit)
	r.done = make(chan struct{})
	if r.dedupe {
		r.inFlight = make(map[string]struct{})
	}

	r.lifecycleWg.Add(4)
	go func() { defer r.lifecycleWg.Done(); r.start() }()
	go func() { defer r.lifecycleWg.Done(); r.createQueue() }()
	go func() { defer r.lifecycleWg.Done(); r.ingest() }()
	go func() { defer r.lifecycleWg.Done(); timeChecker(r.waitCh, r.done) }()

	return r
}
