// SPDX-License-Identifier: MIT

// Basic usage: register two jobs, push a batch, print one tick worth of results.
package main

import (
	"errors"
	"log"
	"time"

	runner "github.com/Sotaneum/go-runner"
)

type Job struct{ id string }

func (j *Job) GetID() string          { return j.id }
func (j *Job) IsRun(t time.Time) bool { return true }
func (j *Job) Run() (any, error)      { return j.id + ":done", nil }

func main() {
	runnerCh := make(chan []runner.JobInterface)
	r := runner.NewRunnerWithLimit(runnerCh, 10)
	defer r.StopAndWait()

	go func() {
		runnerCh <- []runner.JobInterface{&Job{id: "a"}, &Job{id: "b"}}
	}()

	res := <-r.ResultCh
	for _, jr := range res.Items {
		var pe *runner.PanicError
		switch {
		case errors.As(jr.Err, &pe):
			log.Printf("%s panicked: %v", pe.ID, pe.Recovered)
		case jr.Err != nil:
			log.Printf("%s failed: %v", jr.ID, jr.Err)
		default:
			log.Printf("%s -> %v", jr.ID, jr.Value)
		}
	}
}
