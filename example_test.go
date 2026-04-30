// SPDX-License-Identifier: MIT

package runner_test

import (
	"errors"
	"fmt"

	runner "github.com/Sotaneum/go-runner"
)

// ExampleNewRunnerWithLimit demonstrates the option-based constructor.
func ExampleNewRunnerWithLimit() {
	runnerCh := make(chan []runner.JobInterface)
	r := runner.NewRunnerWithLimit(runnerCh, 10,
		runner.WithDedupe(),
		runner.WithBatchBuffer(32),
	)
	defer r.StopAndWait()
	_ = r
}

// ExamplePanicError shows how to distinguish panics from other errors.
func ExamplePanicError() {
	var err error = &runner.PanicError{
		ID:        "demo",
		Recovered: "something went wrong",
	}
	var pe *runner.PanicError
	if errors.As(err, &pe) {
		fmt.Printf("job %s panicked: %v\n", pe.ID, pe.Recovered)
	}
	// Output: job demo panicked: something went wrong
}

// ExampleErrSkippedDuplicate shows how to detect a dedupe skip.
func ExampleErrSkippedDuplicate() {
	jr := runner.JobResult{ID: "x", Err: runner.ErrSkippedDuplicate}
	if errors.Is(jr.Err, runner.ErrSkippedDuplicate) {
		fmt.Println("skipped:", jr.ID)
	}
	// Output: skipped: x
}
