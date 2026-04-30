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

func TestIngestExitsOnRunnerChClose(t *testing.T) {
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5)

	// 호출자가 channel을 닫으면 ingest가 종료되지만 다른 라이프사이클은 살아있다.
	close(ch)
	// ingest 종료를 기다림
	time.Sleep(50 * time.Millisecond)

	// 그 후 Stop은 정상 동작해야 한다 (ingest는 이미 빠졌고, 나머지가 종료됨).
	r.StopAndWait()

	// ResultCh도 정상 닫힘
	select {
	case _, ok := <-r.ResultCh:
		if ok {
			t.Error("ResultCh에 결과가 들어옴")
		}
	case <-time.After(time.Second):
		t.Fatal("ResultCh이 닫히지 않음")
	}
}

func TestPushReturnsFalseAfterStop(t *testing.T) {
	r := New(5)
	r.Stop()
	if ok := r.Push([]JobInterface{}); ok {
		t.Error("Stop 후 Push가 true를 반환")
	}
	r.Wait()
}

func TestDrainBatchesClosesReplyChannels(t *testing.T) {
	// PushAndAwait이 batches에 들어간 직후 Stop이 호출되는 상황 시뮬레이션.
	// 정상 경로로는 직접 재현하기 어려워서 batches에 직접 envelope를 넣고 Stop을 호출.
	ch := make(chan []JobInterface)
	r := NewRunnerWithLimit(ch, 5, WithBatchBuffer(4))

	// batches에 reply가 달린 envelope를 직접 넣는다.
	reply := make(chan Result, 1)
	r.batches <- batchEnvelope{jobs: []JobInterface{}, replyCh: reply}

	// Stop으로 createQueue 종료 + drainBatches 실행
	r.StopAndWait()

	// reply가 닫혔는지 확인
	select {
	case _, ok := <-reply:
		if ok {
			t.Error("reply에 값이 들어옴 (drain은 close만 해야 함)")
		}
	case <-time.After(time.Second):
		t.Fatal("drainBatches가 reply 채널을 닫지 않음")
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
