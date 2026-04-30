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

// JobInterface : Runner 인터페이스입니다.
type JobInterface interface {
	IsRun(t time.Time) bool
	GetID() string
	Run() interface{}
}

// Runner : Runner 객체입니다.
type Runner struct {
	waitCh   chan bool
	nextCh   chan []JobInterface
	queueCh  chan []JobInterface
	ResultCh chan map[string]interface{}
	limit    int
	// sem : 모든 큐가 공유하는 동시성 상한 세마포어. Runner 단위로 1회 생성하여 큐가 겹쳐 실행되더라도 전역 상한이 지켜지도록 한다.
	sem chan struct{}
	// done : Stop()으로 닫히는 종료 신호 채널. 모든 라이프사이클 고루틴이 이 채널을 함께 watch한다.
	done chan struct{}
	// stopOnce : Stop()이 여러 번 호출되어도 done이 한 번만 close되도록 보호한다.
	stopOnce sync.Once
}

// Stop : Runner의 모든 라이프사이클 고루틴(start, createQueue, dispatchRunner, timeChecker)을 종료한다.
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
// 큐 간 동시 실행이 가능하므로 동일 Job ID가 여러 큐에 걸쳐 들어 있으면 중복 실행될 수 있다.
func (r *Runner) runQueue(queue []JobInterface) {
	result := make(map[string]interface{})

	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	for _, item := range queue {
		wg.Add(1)
		// 전역 세마포어: 슬롯이 없으면 슬롯이 빌 때까지 블로킹 대기.
		r.sem <- struct{}{}
		go func(j JobInterface) {
			defer wg.Done()
			defer func() { <-r.sem }()
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

func (r *Runner) createQueue() {
	for {
		// 0초마다 실행하도록 합니다.
		select {
		case <-r.done:
			return
		case <-r.waitCh:
		}
		now := ktime.GetNow()
		var runners []JobInterface
		select {
		case <-r.done:
			return
		case runners = <-r.nextCh:
		}
		queue := []JobInterface{}
		// runner가 지금 실행해야하는 것인지를 확인하고 queue에 추가합니다.
		for _, item := range runners {
			if item.IsRun(now) {
				queue = append(queue, item)
			}
		}
		// Queue를 start함수에 전달합니다.
		select {
		case <-r.done:
			return
		case r.queueCh <- queue:
		}
	}
}

func (r *Runner) dispatchRunner(runnerCh chan []JobInterface) {
	// 빈 Runner 값을 생성합니다.
	prevRunner := []JobInterface{}
	for {
		// 과부하를 방지하기 위해 Second마다 새로운 Runner를 확인합니다.
		select {
		case <-r.done:
			return
		case <-time.After(time.Second):
		}
		select {
		case prevRunner = <-runnerCh:
			// 새로운 Runner가 들어왔을 경우 prevRunner 업데이트합니다.
			select {
			case r.nextCh <- prevRunner:
			default:
			}

		default:
			// 새로운 Runner가 없더라도 기존 Runner를 업데이트합니다.
			select {
			case r.nextCh <- prevRunner:
			default:
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

// NewRunnerWithLimit : 동시 실행 상한값을 지정하여 Runner를 생성합니다.
// limit은 동시에 실행되는 Job 수의 상한이며, 초과분은 슬롯이 빌 때까지 블로킹 대기합니다.
// limit <= 0일 경우 defaultConcurrencyLimit(50)으로 보정됩니다.
// runnerCh가 nil이면 panic합니다.
func NewRunnerWithLimit(runnerCh chan []JobInterface, limit int) *Runner {
	if runnerCh == nil {
		panic("runner: runnerCh must not be nil")
	}
	if limit <= 0 {
		limit = defaultConcurrencyLimit
	}
	r := new(Runner)

	r.waitCh = make(chan bool)
	r.nextCh = make(chan []JobInterface)
	r.queueCh = make(chan []JobInterface)
	r.ResultCh = make(chan map[string]interface{}, resultChBuffer)
	r.limit = limit
	r.sem = make(chan struct{}, limit)
	r.done = make(chan struct{})

	go r.start()
	go r.createQueue()
	go r.dispatchRunner(runnerCh)

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
