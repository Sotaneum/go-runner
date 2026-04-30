// SPDX-License-Identifier: MIT

package runner

import (
	"sync/atomic"
	"testing"
	"time"
)

func TestPushDelivers(t *testing.T) {
	r := New(5)
	defer r.StopAndWait()
	// Push는 ingest를 거쳐 batches로 간다. 분 tick까지 기다리지 않고
	// ingest가 받았는지만 Stats로 확인.
	go r.Push([]JobInterface{&concJob{id: "a"}})
	deadline := time.After(time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("ingest가 batch를 받지 않음")
		default:
		}
		if r.Stats().BatchQueueDepth >= 1 || r.Stats().InFlightJobs > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestPushAndAwaitDelivers(t *testing.T) {
	r := New(5)
	defer r.StopAndWait()
	reply := r.PushAndAwait([]JobInterface{&concJob{id: "a"}, &concJob{id: "b"}})
	select {
	case res, ok := <-reply:
		if !ok {
			t.Fatal("reply channel closed before result delivered")
		}
		if len(res.Items) != 2 {
			t.Errorf("Items = %d, want 2", len(res.Items))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("PushAndAwait reply timeout")
	}
	// 결과 수신 후 reply 채널이 닫히는지 확인
	select {
	case _, ok := <-reply:
		if ok {
			t.Error("reply 채널에 두 번째 값이 들어옴")
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("reply 채널이 닫히지 않음")
	}
}

func TestPushAndAwaitAfterStop(t *testing.T) {
	r := New(5)
	r.Stop()
	reply := r.PushAndAwait([]JobInterface{&concJob{id: "x"}})
	select {
	case _, ok := <-reply:
		if ok {
			t.Error("Stop 후 PushAndAwait이 결과를 반환")
		}
	case <-time.After(time.Second):
		t.Fatal("Stop 후 PushAndAwait이 닫히지 않음")
	}
	r.Wait()
}

func TestWithOnPanicCallback(t *testing.T) {
	var called atomic.Int32
	var capturedID atomic.Pointer[string]
	r := newTestRunner(t, 5)
	r.onPanic = func(id string, p any, stack []byte) {
		called.Add(1)
		capturedID.Store(&id)
	}
	r.queueCh <- batchEnvelope{jobs: []JobInterface{
		&concJob{id: "ok"},
		&panicJob{id: "bad"},
	}}
	<-r.ResultCh
	if called.Load() != 1 {
		t.Errorf("onPanic called %d times, want 1", called.Load())
	}
	if got := capturedID.Load(); got == nil || *got != "bad" {
		t.Errorf("onPanic id captured = %v, want bad", got)
	}
}

func TestWithOnSkipCallback(t *testing.T) {
	var called atomic.Int32
	r := newTestRunner(t, 5)
	r.dedupe = true
	r.inFlight = make(map[string]struct{})
	r.onSkip = func(id string) { called.Add(1) }

	release := make(chan struct{})
	r.queueCh <- batchEnvelope{jobs: []JobInterface{
		&slowCountJob{id: "dup", count: new(atomic.Int32), release: release},
		&slowCountJob{id: "dup", count: new(atomic.Int32), release: release},
	}}
	time.Sleep(50 * time.Millisecond)
	close(release)
	<-r.ResultCh
	if called.Load() != 1 {
		t.Errorf("onSkip called %d times, want 1", called.Load())
	}
}

func TestNewWithOptions(t *testing.T) {
	r := New(5, WithDedupe(), WithBatchBuffer(8))
	defer r.StopAndWait()
	if !r.dedupe {
		t.Error("WithDedupe not applied via New")
	}
	if cap(r.batches) != 8 {
		t.Errorf("batches cap = %d, want 8", cap(r.batches))
	}
}
