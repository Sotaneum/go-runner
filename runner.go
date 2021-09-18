package runner

import (
	"time"

	ktime "github.com/Sotaneum/go-kst-time"
)

// RunnerInterface : Runner 인터페이스입니다.
type RunnerInterface interface {
	IsRun(t time.Time) bool
	GetID() string
	Run() interface{}
}

// Runner : Runner 객체입니다.
type Runner struct {
	waitCh   chan bool
	nextCh   chan []RunnerInterface
	queueCh  chan []RunnerInterface
	ResultCh chan map[string]interface{}
}

func (runner *Runner) start() {
	for {
		// queueCh에 값이 들어오길 대기합니다.
		queue := <-runner.queueCh

		// 결과를 저장할 변수를 생성합니다.
		result := make(map[string]interface{})

		// runner 실행하고 그 결과를 저장합니다.
		for _, item := range queue {
			result[item.GetID()] = item.Run()
		}

		// 실행한 결과를 반환합니다.
		runner.setResult(result)
	}
}

func (runner *Runner) createQueue() {
	for {
		// 0초마다 실행하도록 합니다.
		<-runner.waitCh
		now := ktime.GetNow()
		runners := <-runner.nextCh
		queue := []RunnerInterface{}
		// runner가 지금 실행해야하는 것인지를 확인하고 queue에 추가합니다.
		for _, item := range runners {
			if item.IsRun(now) {
				queue = append(queue, item)
			}
		}
		// Queue를 start함수에 전달합니다.
		runner.queueCh <- queue
	}
}

func (runner *Runner) dispatchRunner(runnerCh chan []RunnerInterface) {
	// 빈 Runner 값을 생성합니다.
	prevRunner := []RunnerInterface{}
	for {
		// 과부하를 방지하기 위해 Second마다 새로운 Runner를 확인합니다.
		time.Sleep(time.Second)
		select {
		case prevRunner = <-runnerCh:
			// 새로운 Runner가 들어왔을 경우 prevRunner 업데이트합니다.
			select {
			case runner.nextCh <- prevRunner:
			default:
			}

		default:
			// 새로운 Runner가 없더라도 기존 Runner를 업데이트합니다.
			select {
			case runner.nextCh <- prevRunner:
			default:
			}
		}
	}
}

func (runner *Runner) setResult(result map[string]interface{}) {
	// ResultCh에 result값을 넣습니다.
	select {
	case runner.ResultCh <- result:
	default:
		// ResultCh을 받지 않더라도 새로운 result가 있을 경우 덮어쓰기합니다.
	}
}

// NewRunner : Runner를 생성합니다. runner.ResultCh 통해 실행 결과를 알 수 있습니다.
func NewRunner(runnerCh chan []RunnerInterface) *Runner {
	runner := new(Runner)

	runner.waitCh = make(chan bool)
	runner.nextCh = make(chan []RunnerInterface)
	runner.queueCh = make(chan []RunnerInterface)
	runner.ResultCh = make(chan map[string]interface{})

	go runner.start()
	go runner.createQueue()
	go runner.dispatchRunner(runnerCh)

	// 0초가 되었을 때 반복할 수 있도록 합니다.
	go timeChecker(runner.waitCh)

	return runner
}

// 매 분마다 이벤트 발생하도록 지정
func timeChecker(waitData chan bool) {
	for {
		time.Sleep(time.Second)
		if time.Now().Second() == 0 {
			select {
			case waitData <- true:
			default:
			}
		}
	}
}
