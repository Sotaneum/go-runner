// SPDX-License-Identifier: MIT

package runner

import (
	"errors"
	"testing"
	"time"
)

func TestRunQueueRecoversPanic(t *testing.T) {
	r := newTestRunner(t, 5)
	queue := []JobInterface{
		&concJob{id: "ok1"},
		&panicJob{id: "bad"},
		&concJob{id: "ok2"},
	}
	r.queueCh <- batchEnvelope{jobs: queue}
	select {
	case res := <-r.ResultCh:
		if len(res.Items) != 3 {
			t.Fatalf("got %d items, want 3", len(res.Items))
		}
		byID := resultByID(res)
		bad := byID["bad"]
		var perr *PanicError
		if !errors.As(bad.Err, &perr) {
			t.Errorf("expected *PanicError for panicked job, got %T: %v", bad.Err, bad.Err)
		} else {
			if perr.ID != "bad" {
				t.Errorf("PanicError.ID = %q, want %q", perr.ID, "bad")
			}
			if len(perr.Stack) == 0 {
				t.Error("PanicError.Stack is empty")
			}
		}
		if byID["ok1"].Value != "ok1" || byID["ok2"].Value != "ok2" {
			t.Errorf("non-panicked jobs should complete normally")
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout — panic likely killed the goroutine")
	}
}

type panicWithErrJob struct {
	id  string
	err error
}

func (j *panicWithErrJob) GetID() string          { return j.id }
func (j *panicWithErrJob) IsRun(t time.Time) bool { return true }
func (j *panicWithErrJob) Run() (any, error)      { panic(j.err) }

func TestPanicErrorUnwrap(t *testing.T) {
	r := newTestRunner(t, 5)
	want := errors.New("io: closed")
	r.queueCh <- batchEnvelope{jobs: []JobInterface{&panicWithErrJob{id: "p", err: want}}}
	res := <-r.ResultCh
	jr := res.Items[0]
	if !errors.Is(jr.Err, want) {
		t.Errorf("errors.Is(jr.Err, want) = false, want true (jr.Err=%v)", jr.Err)
	}
}

func TestRunReturnsErrorPropagated(t *testing.T) {
	r := newTestRunner(t, 5)
	want := errors.New("io: closed")
	r.queueCh <- batchEnvelope{jobs: []JobInterface{&errJob{id: "io", err: want}}}
	res := <-r.ResultCh
	if len(res.Items) != 1 {
		t.Fatalf("got %d items, want 1", len(res.Items))
	}
	jr := res.Items[0]
	if jr.Err == nil || jr.Err.Error() != want.Error() {
		t.Errorf("Err = %v, want %v", jr.Err, want)
	}
	if jr.Value != nil {
		t.Errorf("Value should be nil on error path, got %v", jr.Value)
	}
}
