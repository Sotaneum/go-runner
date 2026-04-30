// SPDX-License-Identifier: MIT

package runner

import (
	"fmt"
	"testing"
	"time"
)

func TestNewRunnerWithLimitFallback(t *testing.T) {
	for _, limit := range []int{0, -1, -100} {
		effective := limit
		if effective <= 0 {
			effective = defaultConcurrencyLimit
		}
		r := &Runner{
			queueCh:  make(chan batchEnvelope),
			ResultCh: make(chan Result, 1),
			limit:    effective,
			sem:      make(chan struct{}, effective),
			done:     make(chan struct{}),
		}
		r.lifecycleWg.Add(1)
		go func() { defer r.lifecycleWg.Done(); r.start() }()
		t.Cleanup(r.StopAndWait)
		r.queueCh <- batchEnvelope{jobs: []JobInterface{&concJob{id: "x"}}}
		select {
		case <-r.ResultCh:
		case <-time.After(time.Second):
			t.Errorf("limit=%d: timeout", limit)
		}
		if r.limit != defaultConcurrencyLimit {
			t.Errorf("limit=%d: got %d, want %d", limit, r.limit, defaultConcurrencyLimit)
		}
	}
}

func TestNewRunnerCompat(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunner(ch)
	defer r.StopAndWait()
	if r.limit != defaultConcurrencyLimit {
		t.Errorf("NewRunner limit = %d, want %d", r.limit, defaultConcurrencyLimit)
	}
}

func TestNewRunnerWithLimitOptions(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5, WithDedupe(), WithBatchBuffer(10))
	defer r.StopAndWait()
	if !r.dedupe {
		t.Error("WithDedupe not applied")
	}
	if r.batchBufferSize != 10 {
		t.Errorf("batchBufferSize = %d, want 10", r.batchBufferSize)
	}
	if cap(r.batches) != 10 {
		t.Errorf("batches cap = %d, want 10", cap(r.batches))
	}
}

func TestWithBatchBufferBackpressure(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 1, WithBatchBuffer(2))
	defer r.StopAndWait()

	for i := 0; i < 3; i++ {
		select {
		case ch <- []JobInterface{&concJob{id: fmt.Sprintf("j%d", i)}}:
		case <-time.After(200 * time.Millisecond):
			t.Fatalf("push %d 블로킹 — 큐가 즉시 수용해야 함", i)
		}
	}
	select {
	case ch <- []JobInterface{&concJob{id: "blocked"}}:
		t.Error("4번째 push가 블로킹되어야 하는데 즉시 수용됨")
	case <-time.After(200 * time.Millisecond):
	}
}
