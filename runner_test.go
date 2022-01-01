package runner_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Sotaneum/go-runner"
)

type data struct {
	id  string
	use bool
}

func (d *data) GetID() string {
	return d.id
}

func (d *data) SetID(id string) {
	d.id = id
}

func (d *data) SetUse(use bool) {
	d.use = use
}

func (d *data) Run() interface{} {
	return "{code:200}"
}

func (d *data) IsRun(t time.Time) bool {
	fmt.Println(t)
	return d.use
}

func TestRunner(t *testing.T) {
	runnerCh := make(chan []runner.JobInterface)
	run := runner.NewRunner(runnerCh)

	runnerObj1 := new(data)
	runnerObj1.SetID("test")
	runnerObj1.SetUse(true)
	runnerObj2 := new(data)
	runnerObj2.SetID("test2")
	runnerObj2.SetUse(false)

	runners := []runner.JobInterface{}
	runners = append(runners, runnerObj1)
	runners = append(runners, runnerObj2)

	runnerCh <- runners

	for {
		result := <-run.ResultCh
		if len(result) != 1 {
			t.Errorf("IsRun func Error")
		}

		for id, value := range result {
			if id != runnerObj1.GetID() {
				t.Errorf("GetID func Error")
			}
			if value != runnerObj1.Run() {
				t.Errorf("Run func Error")
			}
		}
		println("PASS >", time.Now().String())
	}
}
