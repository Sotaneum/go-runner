package runner

import "time"

func (runner *Runner) start() {
	for true {
		// 데이터와 파라미터 데이터를 dispatchData으로 부터 가져옵니다.
		queue := <-runner.queueDataChan
		params := runner.getParams()

		result := make(map[string]interface{})

		// 데이터를 실행하고 그 결과를 저장합니다.
		for _, item := range queue {
			result[item.GetID()] = item.Run(params)
		}

		// 실행한 결과를 반환합니다.
		runner.setResultData(result)
	}
}

func (runner *Runner) createQueue() {
	for true {
		data := runner.getData()
		queue := []RunData{}
		// 해당 데이터가 지금 실행해야하는 데이터인지 확인합니다.
		for _, item := range data {
			if item.IsRun() {
				queue = append(queue, item)
			}
		}
		// Queue 데이터를 start에 전달합니다.
		runner.queueDataChan <- queue

		// 매 0초마다 실행하도록 합니다.
		_ = <-runner.waitChan
	}
}

func (runner *Runner) getData() []RunData {
	return <-runner.nextDataChan
}

func (runner *Runner) dispatchData(dataChan chan []RunData) {
	prevData := []RunData{}
	for true {
		time.Sleep(time.Second)
		select {
		case prevData = <-dataChan:
			select {
			case runner.nextDataChan <- prevData:
				continue
			default:
				continue
			}
		default:
			select {
			case runner.nextDataChan <- prevData:
				continue
			default:
				continue
			}
		}
	}
}

func (runner *Runner) getParams() map[string]string {
	return <-runner.paramChan
}

func (runner *Runner) dispatchParams(paramChan chan map[string]string) {
	prevParams := map[string]string{}
	for true {
		time.Sleep(time.Second)
		select {
		case prevParams = <-paramChan:
			select {
			case runner.paramChan <- prevParams:
				continue
			default:
				continue
			}
		default:
			select {
			case runner.paramChan <- prevParams:
				continue
			default:
				continue
			}
		}
	}
}

func (runner *Runner) setResultData(result map[string]interface{}) {
	select {
	case runner.ResultChan <- result:
		return
	default:
		return
	}
}
