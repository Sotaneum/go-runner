// SPDX-License-Identifier: MIT

package runner

import (
	"errors"
	"fmt"
	"time"
)

// defaultConcurrencyLimit is the default cap on concurrent Run() invocations.
// 외부 서버 보호와 분 단위 스케줄 정확성 사이의 보수적 시작점.
const defaultConcurrencyLimit = 50

// resultChBuffer is the size of the buffered ResultCh.
// 호출자가 잠시 늦어도 결과 손실을 줄이기 위함.
const resultChBuffer = 8

// defaultBatchBuffer is the default capacity of the internal batch FIFO.
// 가득 차면 producer(호출자)가 자연스럽게 블로킹되어 페이싱이 강제된다.
const defaultBatchBuffer = 64

// ErrSkippedDuplicate is reported in JobResult.Err when WithDedupe is enabled
// and a job's GetID() is already in flight.
var ErrSkippedDuplicate = errors.New("runner: skipped duplicate in-flight job")

// PanicError wraps a recovered panic from Run(). It carries the original
// recovered value and the captured stack trace.
type PanicError struct {
	ID        string
	Recovered any
	Stack     []byte
}

func (e *PanicError) Error() string {
	return fmt.Sprintf("runner: job %q panicked: %v", e.ID, e.Recovered)
}

// Unwrap exposes the underlying error if Run() panicked with one (e.g.
// panic(io.EOF)), enabling errors.Is / errors.As against the recovered value.
func (e *PanicError) Unwrap() error {
	if err, ok := e.Recovered.(error); ok {
		return err
	}
	return nil
}

// JobInterface is the contract a runnable job must satisfy.
//
// Run returns the value and an optional error. The value lands in
// JobResult.Value, the error in JobResult.Err. A panic from Run is
// recovered and reported as *PanicError on Err, overriding any return
// value.
type JobInterface interface {
	IsRun(t time.Time) bool
	GetID() string
	Run() (any, error)
}

// JobResult is the outcome of a single job execution.
// When Err is non-nil, Value is unspecified.
type JobResult struct {
	ID        string
	Value     any
	Err       error
	StartedAt time.Time
	EndedAt   time.Time
}

// Result is the aggregated outcome of one queue (one tick).
type Result struct {
	StartedAt time.Time
	EndedAt   time.Time
	Items     []JobResult
}

// ByID indexes Items by JobResult.ID for O(1) lookup.
func (r Result) ByID() map[string]JobResult {
	m := make(map[string]JobResult, len(r.Items))
	for _, it := range r.Items {
		m[it.ID] = it
	}
	return m
}

// Errors returns all non-nil errors keyed by job ID.
func (r Result) Errors() map[string]error {
	out := map[string]error{}
	for _, it := range r.Items {
		if it.Err != nil {
			out[it.ID] = it.Err
		}
	}
	return out
}

// PanicErrors returns only the *PanicError values keyed by job ID.
func (r Result) PanicErrors() map[string]*PanicError {
	out := map[string]*PanicError{}
	for _, it := range r.Items {
		var pe *PanicError
		if errors.As(it.Err, &pe) {
			out[it.ID] = pe
		}
	}
	return out
}

// Stats is a point-in-time snapshot of runner state.
type Stats struct {
	InFlightJobs        int    // 실행 중인 Job 수 (전역 세마포어 사용량)
	BatchQueueDepth     int    // 내부 batch FIFO에 대기 중인 batch 수
	BatchQueueCapacity  int    // 내부 batch FIFO 용량
	DedupeSkipsTotal    uint64 // dedupe로 스킵된 누적 횟수
	DroppedResultsTotal uint64 // ResultCh 버퍼 초과로 드롭된 누적 결과 수
}

// batchEnvelope : 내부 파이프라인을 통과하는 batch 단위 묶음.
// replyCh가 nil이 아니면 PushAndAwait이 만든 1-buffered 채널이며, 결과를 한 번 보내고 닫는다.
type batchEnvelope struct {
	jobs    []JobInterface
	replyCh chan Result
}
