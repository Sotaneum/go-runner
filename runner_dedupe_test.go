// SPDX-License-Identifier: MIT

package runner

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithDedupeSkipsInFlight(t *testing.T) {
	r := newTestRunner(t, 10)
	r.dedupe = true
	r.inFlight = make(map[string]struct{})

	var count atomic.Int32
	release := make(chan struct{})
	queue := []JobInterface{
		&slowCountJob{id: "dup", count: &count, release: release},
		&slowCountJob{id: "dup", count: &count, release: release},
		&slowCountJob{id: "uniq", count: &count, release: release},
	}
	r.queueCh <- batchEnvelope{jobs: queue}
	time.Sleep(50 * time.Millisecond)
	close(release)

	select {
	case res := <-r.ResultCh:
		if len(res.Items) != 3 {
			t.Errorf("expected 3 items (dup OK, dup skipped, uniq OK), got %d: %+v", len(res.Items), res.Items)
		}
		// dup 두 항목 중 하나는 ErrSkippedDuplicate, 다른 하나는 정상 결과
		var dupOK, dupSkipped, uniqOK int
		for _, it := range res.Items {
			switch it.ID {
			case "dup":
				if errors.Is(it.Err, ErrSkippedDuplicate) {
					dupSkipped++
				} else if it.Err == nil {
					dupOK++
				}
			case "uniq":
				if it.Err == nil {
					uniqOK++
				}
			}
		}
		if dupOK != 1 || dupSkipped != 1 || uniqOK != 1 {
			t.Errorf("dupOK=%d dupSkipped=%d uniqOK=%d, want 1/1/1", dupOK, dupSkipped, uniqOK)
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh timeout")
	}
	if c := count.Load(); c != 2 {
		t.Errorf("Run() invocations = %d, want 2", c)
	}
	if r.dedupeSkips.Load() != 1 {
		t.Errorf("dedupeSkips counter = %d, want 1", r.dedupeSkips.Load())
	}
}

func TestInFlightIDs(t *testing.T) {
	r := newTestRunner(t, 5)
	r.dedupe = true
	r.inFlight = make(map[string]struct{})

	release := make(chan struct{})
	r.queueCh <- batchEnvelope{jobs: []JobInterface{
		&slowCountJob{id: "x", count: new(atomic.Int32), release: release},
		&slowCountJob{id: "y", count: new(atomic.Int32), release: release},
	}}
	// Job들이 release 대기로 들어갈 시간 확보
	time.Sleep(50 * time.Millisecond)
	ids := r.InFlightIDs()
	if len(ids) != 2 {
		t.Errorf("InFlightIDs len = %d, want 2 (got %v)", len(ids), ids)
	}
	close(release)
	<-r.ResultCh
	if got := r.InFlightIDs(); len(got) != 0 {
		t.Errorf("after completion InFlightIDs = %v, want empty", got)
	}
}
