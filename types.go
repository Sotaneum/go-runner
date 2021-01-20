package runner

// RunData : RunData 인터페이스입니다.
type RunData interface {
	IsRun() bool
	GetID() string
	Run(map[string]string) interface{}
}

// Runner : Runner 객체입니다.
type Runner struct {
	waitChan      chan bool
	nextDataChan  chan []RunData
	queueDataChan chan []RunData
	paramChan     chan map[string]string
	ResultChan    chan map[string]interface{}
}
