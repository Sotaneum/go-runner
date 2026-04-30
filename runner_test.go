package runner_test

import (
	"testing"
	"time"

	"github.com/Sotaneum/go-runner"
)

type data struct {
	id  string
	use bool
}

func (d *data) GetID() string          { return d.id }
func (d *data) IsRun(t time.Time) bool { return d.use }
func (d *data) Run() any               { return "{code:200}" }

func TestNewRunner(t *testing.T) {
	ch := make(chan []runner.JobInterface)
	r := runner.NewRunner(ch)
	defer r.Stop()
	if r == nil {
		t.Fatal("NewRunner returned nil")
	}
	if r.ResultCh == nil {
		t.Fatal("ResultCh not initialized")
	}
}

func TestNewRunnerWithLimit(t *testing.T) {
	ch := make(chan []runner.JobInterface)
	r := runner.NewRunnerWithLimit(ch, 10)
	defer r.Stop()
	if r == nil {
		t.Fatal("NewRunnerWithLimit returned nil")
	}
}

func TestStopIdempotent(t *testing.T) {
	ch := make(chan []runner.JobInterface)
	r := runner.NewRunnerWithLimit(ch, 5)

	done := make(chan struct{})
	go func() {
		r.Stop()
		r.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Stop이 시간 내 반환되지 않음")
	}
}

func TestNewRunnerWithLimitNilChan(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("expected panic on nil runnerCh")
		}
	}()
	runner.NewRunnerWithLimit(nil, 10)
}
