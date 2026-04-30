package runner

import (
	"fmt"
	"sync"
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

// JobInterface : Runner 인터페이스입니다.
type JobInterface interface {
	IsRun(t time.Time) bool
	GetID() string
	Run() interface{}
}

// Option : NewRunnerWithLimit에 주입 가능한 동작 변경 옵션.
type Option func(*Runner)

// WithDedupe : 동일 Job ID가 in-flight일 때 중복 실행을 막는다.
// 같은 ID가 여러 큐 또는 한 큐 안에 여러 번 등장해도 동시 실행은 1회로 제한된다.
// 스킵된 Job은 결과 맵에 포함되지 않는다.
func WithDedupe() Option {
	return func(r *Runner) {
		r.dedupe = true
	}
}

// WithBatchBuffer : runnerCh로 들어온 batch를 보관하는 내부 FIFO 큐의 크기를 지정한다.
// 큐가 가득 차면 호출자가 runnerCh에 push할 때 블로킹되어 자연스러운 backpressure를 형성한다.
// n < 1이면 1로 보정된다. 기본값은 defaultBatchBuffer.
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
	ResultCh chan map[string]interface{}
	limit    int
	// sem : 모든 큐가 공유하는 동시성 상한 세마포어. Runner 단위로 1회 생성하여 큐가 겹쳐 실행되더라도 전역 상한이 지켜지도록 한다.
	sem chan struct{}
	// done : Stop()으로 닫히는 종료 신호 채널. 모든 라이프사이클 고루틴이 이 채널을 함께 watch한다.
	done chan struct{}
	// stopOnce : Stop()이 여러 번 호출되어도 done이 한 번만 close되도록 보호한다.
	stopOnce sync.Once

	// 옵션 필드
	batchBufferSize int
	dedupe          bool

	// dedupe용 in-flight 추적
	inFlight   map[string]struct{}
	inFlightMu sync.Mutex
}

// Stop : Runner의 모든 라이프사이클 고루틴(start, createQueue, ingest, timeChecker)을 종료한다.
// 이미 시작된 runQueue / Job 고루틴은 자체 완료까지 진행되며 강제 취소되지 않는다 (현재 Run()에 컨텍스트 없음).
// 여러 번 호출해도 안전(idempotent).
func (r *Runner) Stop() {
	r.stopOnce.Do(func() { close(r.done) })
}

// start : queueCh에서 큐를 받자마자 별도 고루틴으로 처리를 위임하고 즉시 다음 큐를 받는다.
// 이전 큐 처리에 묶여 createQueue가 다음 분 tick에서 막히는 문제를 회피하기 위함.
func (r *Runner) start() {
	for {
		select {
		case <-r.done:
			return
		case queue := <-r.queueCh:
			go r.runQueue(queue)
		}
	}
}

// runQueue : 한 큐를 동시 실행하고 결과를 ResultCh로 보낸다.
// dedupe 모드에서는 in-flight인 ID를 만나면 그 항목을 스킵한다.
func (r *Runner) runQueue(queue []JobInterface) {
	result := make(map[string]interface{})

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	for _, item := range queue {
		if r.dedupe && !r.markInFlight(item.GetID()) {
			// 이미 in-flight인 ID — 스킵
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
			// 한 Job의 패닉이 프로세스 전체를 죽이지 않도록 라이브러리 레벨에서 recover.
			// 패닉은 결과 맵에 error 타입으로 기록되어 호출자가 식별할 수 있다.
			defer func() {
				if p := recover(); p != nil {
					mu.Lock()
					result[j.GetID()] = fmt.Errorf("panic: %v", p)
					mu.Unlock()
				}
			}()
			out := j.Run()
			mu.Lock()
			result[j.GetID()] = out
			mu.Unlock()
		}(item)
	}
	wg.Wait()

	r.setResult(result)
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

// createQueue : 매 분 tick마다 batch FIFO 큐에서 가장 오래된 batch 1개를 꺼내
// IsRun(now) 평가 후 통과한 Job들을 queueCh로 넘긴다.
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
			// 처리할 batch 없음 — 이번 tick은 건너뛴다
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

// ingest : runnerCh로 들어오는 batch를 즉시 받아 내부 FIFO 큐(r.batches)에 넣는다.
// 내부 큐가 가득 차면 호출자가 runnerCh로 push할 때 블로킹되어 자연 backpressure가 발생한다.
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

func (r *Runner) setResult(result map[string]interface{}) {
	// ResultCh에 result값을 넣습니다.
	select {
	case r.ResultCh <- result:
	default:
		// ResultCh을 받지 않더라도 새로운 result가 있을 경우 덮어쓰기합니다.
	}
}

// NewRunner : Runner를 생성합니다. runner.ResultCh 통해 실행 결과를 알 수 있습니다.
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
	r.ResultCh = make(chan map[string]interface{}, resultChBuffer)
	r.sem = make(chan struct{}, limit)
	r.done = make(chan struct{})
	if r.dedupe {
		r.inFlight = make(map[string]struct{})
	}

	go r.start()
	go r.createQueue()
	go r.ingest(runnerCh)

	// 매 분 정각에 이벤트 발생하도록 지정
	go timeChecker(r.waitCh, r.done)

	return r
}

// timeChecker : 매 분 정각(sec==0)에 waitData로 신호를 보낸다.
// 다음 분 정각까지 정확히 sleep하여 누적 drift 없이 분을 놓치지 않도록 한다.
// done이 닫히면 종료한다.
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
