// SPDX-License-Identifier: MIT

package runner

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartConcurrent(t *testing.T) {
	r := newTestRunner(t, 50)
	queue := make([]JobInterface, 10)
	for i := range queue {
		queue[i] = &concJob{id: fmt.Sprintf("j%d", i), sleep: 100 * time.Millisecond}
	}
	start := time.Now()
	r.queueCh <- batchEnvelope{jobs: queue}
	<-r.ResultCh
	elapsed := time.Since(start)
	// 직렬이면 1000ms, 동시면 ~100ms. 느린 CI 마진 포함 500ms.
	if elapsed > 500*time.Millisecond {
		t.Errorf("expected concurrent execution under 500ms, got %v", elapsed)
	}
}

func TestStartRespectsLimit(t *testing.T) {
	var running, maxSeen atomic.Int32
	r := newTestRunner(t, 3)
	queue := make([]JobInterface, 10)
	for i := range queue {
		queue[i] = &concJob{
			id:      fmt.Sprintf("j%d", i),
			sleep:   50 * time.Millisecond,
			running: &running,
			maxSeen: &maxSeen,
		}
	}
	r.queueCh <- batchEnvelope{jobs: queue}
	<-r.ResultCh
	if m := maxSeen.Load(); m > 3 {
		t.Errorf("max concurrent = %d, want <= 3", m)
	}
}

func TestStartResultCollection(t *testing.T) {
	r := newTestRunner(t, 20)
	queue := make([]JobInterface, 100)
	for i := range queue {
		queue[i] = &concJob{id: fmt.Sprintf("j%d", i)}
	}
	r.queueCh <- batchEnvelope{jobs: queue}
	res := <-r.ResultCh
	if len(res.Items) != 100 {
		t.Fatalf("got %d items, want 100", len(res.Items))
	}
	byID := resultByID(res)
	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("j%d", i)
		jr, ok := byID[id]
		if !ok {
			t.Errorf("missing result for %s", id)
			continue
		}
		if jr.Err != nil {
			t.Errorf("%s: unexpected err %v", id, jr.Err)
		}
		if jr.Value != id {
			t.Errorf("%s: value=%v want %s", id, jr.Value, id)
		}
	}
}

func TestStartAcceptsNextQueueWhileBusy(t *testing.T) {
	r := newTestRunner(t, 1)
	slow := []JobInterface{&concJob{id: "slow", sleep: 300 * time.Millisecond}}
	fast := []JobInterface{&concJob{id: "fast"}}

	r.queueCh <- batchEnvelope{jobs: slow}
	select {
	case r.queueCh <- batchEnvelope{jobs: fast}:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("start()가 다음 큐를 즉시 수신하지 못함")
	}
	got := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case res := <-r.ResultCh:
			for _, it := range res.Items {
				got[it.ID] = true
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("ResultCh 수신 timeout (i=%d)", i)
		}
	}
	if !got["slow"] || !got["fast"] {
		t.Errorf("두 큐 결과 모두 수신해야 함, got=%v", got)
	}
}

func TestGlobalLimitAcrossQueues(t *testing.T) {
	var running, maxSeen atomic.Int32
	r := newTestRunner(t, 3)
	mkQueue := func(prefix string, n int) []JobInterface {
		q := make([]JobInterface, n)
		for i := range q {
			q[i] = &concJob{
				id:      fmt.Sprintf("%s-%d", prefix, i),
				sleep:   80 * time.Millisecond,
				running: &running,
				maxSeen: &maxSeen,
			}
		}
		return q
	}
	r.queueCh <- batchEnvelope{jobs: mkQueue("a", 10)}
	time.Sleep(10 * time.Millisecond)
	r.queueCh <- batchEnvelope{jobs: mkQueue("b", 10)}
	for i := 0; i < 2; i++ {
		select {
		case <-r.ResultCh:
		case <-time.After(5 * time.Second):
			t.Fatalf("timeout (i=%d)", i)
		}
	}
	if m := maxSeen.Load(); m > 3 {
		t.Errorf("global concurrent = %d, want <= 3", m)
	}
}
