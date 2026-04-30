// SPDX-License-Identifier: MIT

package runner

import (
	"errors"
	"fmt"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	ktime "github.com/Sotaneum/go-kst-time"
)

// defaultConcurrencyLimit : 외부 서버 보호와 분 단위 스케줄 정확성 사이의 보수적 시작점
const defaultConcurrencyLimit = 50

// resultChBuffer : ResultCh의 버퍼 크기. 호출자가 잠시 늦어도 결과 손실을 줄이기 위함.
const resultChBuffer = 8

// defaultBatchBuffer : runnerCh로 들어온 batch를 누적하는 내부 FIFO 큐의 기본 크기.
// 큐가 가득 차면 producer(호출자)가 자연스럽게 블로킹되어 페이싱이 강제된다.
const defaultBatchBuffer = 64

// ErrSkippedDuplicate : WithDedupe 모드에서 동일 ID가 in-flight여서 스킵된 Job의 결과 에러.
var ErrSkippedDuplicate = errors.New("runner: skipped duplicate in-flight job")

// PanicError : Run()에서 발생한 패닉을 감싸는 에러. Recovered와 Stack을 포함한다.
type PanicError struct {
	ID        string
	Recovered any
	Stack     []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("runner: job %q panicked: %v", e.ID, e.Recovered)
}

// JobInterface : Runner 인터페이스입니다.
// Run은 결과 값과 에러를 함께 반환합니다. 에러는 JobResult.Err에 그대로 저장되며,
// Run 내부에서 패닉이 발생하면 Run의 반환값은 무시되고 Err에 *PanicError가 들어갑니다.
type JobInterface interface {
	IsRun(t time.Time) bool
	GetID() string
	Run() (any, error)
}

// JobResult : 한 Job의 실행 결과.
// Err가 non-nil이면 Value는 무시한다 (패닉 또는 dedupe 스킵 등).
// Err가 nil이면 Value는 Run()의 반환값.
type JobResult struct {
	ID        string
	Value     any
	Err       error
	StartedAt time.Time
	EndedAt   time.Time
}

// Result : 한 큐의 실행 결과 묶음.
type Result struct {
	StartedAt time.Time
	EndedAt   time.Time
	Items     []JobResult
}

// Stats : Runner 운영 상태 스냅샷.
type Stats struct {
	InFlightJobs        int    // 실행 중인 Job 수 (전역 세마포어 사용량)
	BatchQueueDepth     int    // 내부 batch FIFO에 대기 중인 batch 수
	BatchQueueCapacity  int    // 내부 batch FIFO 용량
	DedupeSkipsTotal    uint64 // dedupe로 스킵된 누적 횟수
	DroppedResultsTotal uint64 // ResultCh 버퍼 초과로 드롭된 누적 결과 수
}

// Option : NewRunnerWithLimit에 주입 가능한 동작 변경 옵션.
type Option func(*Runner)

// WithDedupe : 동일 Job ID가 in-flight일 때 중복 실행을 막는다.
// 스킵된 Job은 결과의 JobResult.Err에 ErrSkippedDuplicate로 표시된다.
func WithDedupe() Option {
	return func(r *Runner) { r.dedupe = true }
}

// WithBatchBuffer : runnerCh로 들어온 batch를 보관하는 내부 FIFO 큐의 크기를 지정한다.
// 큐가 가득 차면 호출자가 runnerCh에 push할 때 블로킹되어 자연 backpressure를 형성한다.
// n < 1이면 1로 보정. 기본값은 defaultBatchBuffer(64).
func WithBatchBuffer(n int) Option {
	return func(r *Runner) {
		if n < 1 {
			n = 1
		}
		r.batchBufferSize = n
	}
}

// Runner : Runner 객체입니다.
type Runner struct {
	waitCh   chan bool
	queueCh  chan []JobInterface
	batches  chan []JobInterface
	ResultCh chan Result
	limit    int
	// sem : 모든 큐가 공유하는 동시성 상한 세마포어. Runner 단위로 1회 생성하여 큐가 겹쳐 실행되더라도 전역 상한이 지켜지도록 한다.
	sem chan struct{}
	// done : Stop()으로 닫히는 종료 신호 채널.
	done chan struct{}
	// stopOnce : Stop()이 여러 번 호출되어도 done이 한 번만 close되도록 보호한다.
	stopOnce sync.Once
	// closeResultOnce : ResultCh를 한 번만 닫도록 보호한다.
	closeResultOnce sync.Once
	// lifecycleWg : start, createQueue, ingest, timeChecker 라이프사이클 고루틴 추적.
	lifecycleWg sync.WaitGroup
	// inFlightQueues : 실행 중인 runQueue 고루틴 추적. Wait()가 사용한다.
	inFlightQueues sync.WaitGroup

	// 옵션 필드
	batchBufferSize int
	dedupe          bool

	// dedupe용 in-flight 추적
	inFlight   map[string]struct{}
	inFlightMu sync.Mutex

	// 카운터
	dedupeSkips    atomic.Uint64
	droppedResults atomic.Uint64
}

// Stop : Runner의 라이프사이클 고루틴(start, createQueue, ingest, timeChecker)을 종료한다.
// 라이프사이클 고루틴이 모두 종료될 때까지 동기적으로 대기한다.
// 이미 시작된 runQueue / Job 고루틴은 자체 완료까지 진행되며 강제 취소되지 않으므로,
// in-flight 작업까지 마무리하려면 Wait() 또는 StopAndWait()을 사용한다.
// 여러 번 호출해도 안전(idempotent).
func (r *Runner) Stop() {
	r.stopOnce.Do(func() {
		close(r.done)
		r.lifecycleWg.Wait()
	})
}

// InFlightIDs : 현재 실행 중인 Job ID 목록을 반환한다 (WithDedupe 모드에서만 의미 있음).
// dedupe가 비활성화된 경우 항상 빈 슬라이스를 반환한다.
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

// Wait : 현재 in-flight인 모든 runQueue가 완료될 때까지 대기하고 ResultCh를 닫는다.
// 보통 Stop 이후에 호출되며, 호출 후에는 `range r.ResultCh` 루프가 자연스럽게 종료된다.
// ResultCh는 첫 호출에서만 닫히므로 여러 번 호출해도 안전하다.
func (r *Runner) Wait() {
	r.inFlightQueues.Wait()
	r.closeResultOnce.Do(func() { close(r.ResultCh) })
}

// StopAndWait : Stop을 호출한 뒤 in-flight runQueue 완료를 기다리고 ResultCh를 닫는다.
func (r *Runner) StopAndWait() {
	r.Stop()
	r.Wait()
}

// Stats : 현재 운영 상태의 스냅샷을 반환한다.
func (r *Runner) Stats() Stats {
	return Stats{
		InFlightJobs:        len(r.sem),
		BatchQueueDepth:     len(r.batches),
		BatchQueueCapacity:  cap(r.batches),
		DedupeSkipsTotal:    r.dedupeSkips.Load(),
		DroppedResultsTotal: r.droppedResults.Load(),
	}
}

func (r *Runner) start() {
	for {
		select {
		case <-r.done:
			return
		case queue := <-r.queueCh:
			r.inFlightQueues.Add(1)
			go func(q []JobInterface) {
				defer r.inFlightQueues.Done()
				r.runQueue(q)
			}(queue)
		}
	}
}

// runQueue : 한 큐를 동시 실행하고 Result를 ResultCh로 보낸다.
func (r *Runner) runQueue(queue []JobInterface) {
	startedAt := time.Now()
	items := make([]JobResult, 0, len(queue))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	appendResult := func(jr JobResult) {
		mu.Lock()
		items = append(items, jr)
		mu.Unlock()
	}

	for _, item := range queue {
		if r.dedupe && !r.markInFlight(item.GetID()) {
			r.dedupeSkips.Add(1)
			appendResult(JobResult{
				ID:        item.GetID(),
				Err:       ErrSkippedDuplicate,
				StartedAt: time.Now(),
				EndedAt:   time.Now(),
			})
			continue
		}
		wg.Add(1)
		// 전역 세마포어: 슬롯이 없으면 슬롯이 빌 때까지 블로킹 대기.
		r.sem <- struct{}{}
		go func(j JobInterface) {
			defer wg.Done()
			defer func() { <-r.sem }()
			if r.dedupe {
				defer r.unmarkInFlight(j.GetID())
			}
			started := time.Now()
			var (
				out any
				err error
			)
			func() {
				// 한 Job의 패닉이 프로세스 전체를 죽이지 않도록 라이브러리 레벨에서 recover.
				// 패닉은 Run의 정상 반환 에러를 덮어쓴다.
				defer func() {
					if p := recover(); p != nil {
						out = nil
						err = &PanicError{
							ID:        j.GetID(),
							Recovered: p,
							Stack:     debug.Stack(),
						}
					}
				}()
				out, err = j.Run()
			}()
			appendResult(JobResult{
				ID:        j.GetID(),
				Value:     out,
				Err:       err,
				StartedAt: started,
				EndedAt:   time.Now(),
			})
		}(item)
	}
	wg.Wait()

	r.setResult(Result{
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Items:     items,
	})
}

// markInFlight : id가 이미 in-flight면 false, 새로 등록되면 true.
func (r *Runner) markInFlight(id string) bool {
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	if _, exists := r.inFlight[id]; exists {
		return false
	}
	r.inFlight[id] = struct{}{}
	return true
}

func (r *Runner) unmarkInFlight(id string) {
	r.inFlightMu.Lock()
	delete(r.inFlight, id)
	r.inFlightMu.Unlock()
}

// createQueue : 매 분 tick마다 batch FIFO에서 1개를 pop하고 IsRun(now) 평가 후 queueCh로 전달.
// 큐에 batch가 없으면 그 tick은 스킵한다.
func (r *Runner) createQueue() {
	for {
		select {
		case <-r.done:
			return
		case <-r.waitCh:
		}
		var batch []JobInterface
		select {
		case <-r.done:
			return
		case batch = <-r.batches:
		default:
			continue
		}
		now := ktime.GetNow()
		queue := []JobInterface{}
		for _, item := range batch {
			if item.IsRun(now) {
				queue = append(queue, item)
			}
		}
		select {
		case <-r.done:
			return
		case r.queueCh <- queue:
		}
	}
}

// ingest : runnerCh로 들어오는 batch를 즉시 받아 내부 FIFO(r.batches)에 넣는다.
// 내부 큐가 가득 차면 호출자가 runnerCh로 push할 때 블로킹되어 자연 backpressure가 발생한다.
// 호출자가 runnerCh를 닫으면 ingest가 종료되지만 다른 라이프사이클 고루틴은 계속 실행되므로
// 완전한 정리를 위해서는 Stop()을 호출해야 한다.
func (r *Runner) ingest(runnerCh chan []JobInterface) {
	for {
		select {
		case <-r.done:
			return
		case b, ok := <-runnerCh:
			if !ok {
				return
			}
			select {
			case <-r.done:
				return
			case r.batches <- b:
			}
		}
	}
}

// setResult : Result를 ResultCh로 보낸다. 채널 버퍼가 가득 차면 가장 오래된 결과가 드롭된다.
func (r *Runner) setResult(res Result) {
	for {
		select {
		case r.ResultCh <- res:
			return
		default:
			// 버퍼 가득. 가장 오래된 결과를 비워서 새 결과 자리를 만든다.
			select {
			case <-r.ResultCh:
				r.droppedResults.Add(1)
			default:
				// 동시에 다른 곳에서 비웠을 수 있음. 다시 시도.
			}
		}
	}
}

// NewRunner : Runner를 생성합니다. ResultCh 통해 실행 결과를 알 수 있습니다.
// 동시 실행 상한은 defaultConcurrencyLimit(50)이 적용됩니다.
func NewRunner(runnerCh chan []JobInterface) *Runner {
	return NewRunnerWithLimit(runnerCh, defaultConcurrencyLimit)
}

// NewRunnerWithLimit : 동시 실행 상한값과 옵션을 지정하여 Runner를 생성합니다.
// limit은 동시에 실행되는 Job 수의 상한이며, 초과분은 슬롯이 빌 때까지 블로킹 대기합니다.
// limit <= 0일 경우 defaultConcurrencyLimit(50)으로 보정됩니다.
// runnerCh가 nil이면 panic합니다.
// 옵션: WithDedupe(), WithBatchBuffer(n).
func NewRunnerWithLimit(runnerCh chan []JobInterface, limit int, opts ...Option) *Runner {
	if runnerCh == nil {
		panic("runner: runnerCh must not be nil")
	}
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := &Runner{
		limit:           limit,
		batchBufferSize: defaultBatchBuffer,
	}
	for _, o := range opts {
		o(r)
	}

	r.waitCh = make(chan bool)
	r.queueCh = make(chan []JobInterface)
	r.batches = make(chan []JobInterface, r.batchBufferSize)
	r.ResultCh = make(chan Result, resultChBuffer)
	r.sem = make(chan struct{}, limit)
	r.done = make(chan struct{})
	if r.dedupe {
		r.inFlight = make(map[string]struct{})
	}

	r.lifecycleWg.Add(4)
	go func() { defer r.lifecycleWg.Done(); r.start() }()
	go func() { defer r.lifecycleWg.Done(); r.createQueue() }()
	go func() { defer r.lifecycleWg.Done(); r.ingest(runnerCh) }()
	go func() { defer r.lifecycleWg.Done(); timeChecker(r.waitCh, r.done) }()

	return r
}

// timeChecker : 매 분 정각에 waitData로 신호를 보낸다.
// 다음 분 정각까지 정확히 sleep하여 누적 drift 없이 분을 놓치지 않도록 한다.
func timeChecker(waitData chan bool, done <-chan struct{}) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-done:
			return
		case <-time.After(time.Until(next)):
		}
		select {
		case waitData <- true:
		case <-done:
			return
		default:
		}
	}
}
