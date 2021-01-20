package runner

import "time"

// NewRunner : Runner를 생성합니다. ResultChan을 통해 실행 결과를 알 수 있습니다.
func NewRunner(dataChan chan []RunData, paramDataChan chan map[string]string) *Runner {
	runner := new(Runner)

	runner.waitChan = make(chan bool)
	runner.nextDataChan = make(chan []RunData)
	runner.queueDataChan = make(chan []RunData)
	runner.paramChan = make(chan map[string]string)
	runner.ResultChan = make(chan map[string]interface{})

	go runner.start()
	go runner.createQueue()
	go runner.dispatchData(dataChan)
	go runner.dispatchParams(paramDataChan)

	// 0초가 되었을 때 반복할 수 있도록 합니다.
	go timeChecker(runner.waitChan)

	return runner
}

// 매 분마다 이벤트 발생하도록 지정
func timeChecker(waitData chan bool) {
	for true {
		time.Sleep(time.Second)
		if time.Now().Second() == 0 {
			select {
			case waitData <- true:
				continue
			default:
				continue
			}
		}
	}
}
