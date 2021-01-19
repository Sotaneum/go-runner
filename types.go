package runner

// RunData : RunData 인터페이스입니다.
type RunData interface {
	Run(interface{}) interface{}
	IsRun() bool
	GetID() string
}

// Runner : Runner 객체입니다.
type Runner struct {
	nextDataChan  chan []RunData
	queueDataChan chan []RunData
	paramDataChan chan map[string]string
	ResultChan    chan map[string]interface{}
}
