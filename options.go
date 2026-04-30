// SPDX-License-Identifier: MIT

package runner

// Option configures a Runner at construction time.
type Option func(*Runner)

// WithDedupe prevents the same GetID() from running concurrently.
// A duplicate is reported as JobResult{Err: ErrSkippedDuplicate} and the
// dedupe skip counter (Stats().DedupeSkipsTotal) is incremented.
func WithDedupe() Option {
	return func(r *Runner) { r.dedupe = true }
}

// WithBatchBuffer sets the size of the internal batch FIFO.
// n < 1 is normalized to 1. Default is 64.
//
// When the buffer is full, sends to runnerCh (or Push) block, providing
// natural backpressure.
func WithBatchBuffer(n int) Option {
	return func(r *Runner) {
		if n < 1 {
			n = 1
		}
		r.batchBufferSize = n
	}
}

// WithOnPanic registers a callback invoked when Run() panics.
// The callback runs synchronously in the job goroutine, so it should be cheap.
func WithOnPanic(fn func(id string, recovered any, stack []byte)) Option {
	return func(r *Runner) { r.onPanic = fn }
}

// WithOnSkip registers a callback invoked when a job is skipped by WithDedupe.
// The callback runs synchronously, so it should be cheap.
func WithOnSkip(fn func(id string)) Option {
	return func(r *Runner) { r.onSkip = fn }
}
