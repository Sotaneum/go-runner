// SPDX-License-Identifier: MIT

package runner

import (
	"runtime"
	"testing"
	"time"
)

func TestStopReleasesGoroutines(t *testing.T) {
	baseline := runtime.NumGoroutine()
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)
	t.Cleanup(r.StopAndWait)

	time.Sleep(50 * time.Millisecond)
	if delta := runtime.NumGoroutine() - baseline; delta < 4 {
		t.Fatalf("expected at least 4 new goroutines, got delta=%d", delta)
	}
	r.Stop()
	// Stop은 동기적으로 라이프사이클 종료를 대기하므로, 즉시 검사 가능.
	if runtime.NumGoroutine() > baseline+2 {
		t.Errorf("goroutines did not exit after Stop: baseline=%d, current=%d", baseline, runtime.NumGoroutine())
	}
}

func TestStopAndWait(t *testing.T) {
	// Wait의 in-flight 대기 의미를 검증: 직접 inFlightQueues에 Add/Done하여
	// 같은 테스트 고루틴 내에서 happens-before를 명확히 한 뒤 StopAndWait이
	// Done 호출까지 블로킹되는지 확인한다.
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)

	r.inFlightQueues.Add(1)
	go func() {
		time.Sleep(150 * time.Millisecond)
		r.inFlightQueues.Done()
	}()

	start := time.Now()
	r.StopAndWait()
	elapsed := time.Since(start)
	if elapsed < 100*time.Millisecond {
		t.Errorf("StopAndWait이 in-flight 작업을 기다리지 않음: elapsed=%v", elapsed)
	}
}

func TestStatsSnapshot(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5, WithDedupe(), WithBatchBuffer(8))
	defer r.StopAndWait()
	s := r.Stats()
	if s.BatchQueueCapacity != 8 {
		t.Errorf("BatchQueueCapacity = %d, want 8", s.BatchQueueCapacity)
	}
	if s.InFlightJobs != 0 || s.BatchQueueDepth != 0 {
		t.Errorf("초기 상태 비어있어야 함, got %+v", s)
	}
}

func TestResultChClosedAfterStopAndWait(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)
	r.StopAndWait()
	select {
	case _, ok := <-r.ResultCh:
		if ok {
			t.Error("ResultCh에서 결과가 수신됨 (큐를 보내지 않았는데)")
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh이 StopAndWait 후 닫히지 않음")
	}
}
