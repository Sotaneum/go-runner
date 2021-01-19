package runner

import "time"

func (runner *Runner) start() {
	for true {
		var result map[string]interface{}

		// 데이터와 파라미터 데이터를 dispatchData으로 부터 가져옵니다.
		queue := <-runner.queueDataChan
		params := <-runner.paramDataChan

		// 데이터를 실행하고 그 결과를 저장합니다.
		for _, item := range queue {
			result[item.GetID()] = item.Run(params)
		}

		// 실행한 결과를 반환합니다.
		runner.ResultChan <- result
	}
}

func (runner *Runner) createQueue() {
	// 0초가 되었을 때 반복할 수 있도록 합니다.
	waitData := make(chan bool)
	go timeChecker(waitData)

	for true {
		data := <-runner.nextDataChan
		queue := []RunData{}

		// 해당 데이터가 지금 실행해야하는 데이터인지 확인합니다.
		for _, item := range data {
			if item.IsRun() {
				queue = append(queue, item)
			}
		}

		// Queue 데이터를 start에 전달합니다.
		runner.queueDataChan <- queue
		_ = <-waitData
	}
}

func (runner *Runner) dispatchData(dataChan chan []RunData, paramDataChan chan map[string]string) {
	prevData := []RunData{}
	prevParamData := map[string]string{}

	for true {
		time.Sleep(time.Second)
		select {
		/* 데이터를 업데이트합니다. */
		case prevData = <-dataChan:
			select {
			case runner.nextDataChan <- prevData:
			default:
			}
		/* 어느 값도 업데이트 할 수 없다면 기본 값으로 처리합니다. */
		default:
			select {
			case runner.nextDataChan <- prevData:
			default:
			}
		}
		select {
		/* 파라미터 데이터를 업데이트합니다. */
		case prevParamData = <-paramDataChan:
			select {
			case runner.paramDataChan <- prevParamData:
			default:
			}
		/* 어느 값도 업데이트 할 수 없다면 기본 값으로 처리합니다. */
		default:
			select {
			case runner.paramDataChan <- prevParamData:
			default:
			}
		}

	}
}
