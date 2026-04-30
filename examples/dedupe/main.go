// SPDX-License-Identifier: MIT

// Dedupe: same ID across overlapping queues runs at most once concurrently.
package main

import (
	"errors"
	"log"
	"time"

	runner "github.com/Sotaneum/go-runner"
)

type Job struct {
	id    string
	sleep time.Duration
}

func (j *Job) GetID() string          { return j.id }
func (j *Job) IsRun(t time.Time) bool { return true }
func (j *Job) Run() (any, error) {
	time.Sleep(j.sleep)
	return j.id, nil
}

func main() {
	runnerCh := make(chan []runner.JobInterface)
	r := runner.NewRunnerWithLimit(runnerCh, 10, runner.WithDedupe())
	defer r.StopAndWait()

	go func() {
		// 같은 ID "slow"가 두 큐에 등장.
		runnerCh <- []runner.JobInterface{&Job{id: "slow", sleep: 200 * time.Millisecond}}
		runnerCh <- []runner.JobInterface{&Job{id: "slow", sleep: 200 * time.Millisecond}}
	}()

	res := <-r.ResultCh
	for _, jr := range res.Items {
		if errors.Is(jr.Err, runner.ErrSkippedDuplicate) {
			log.Printf("%s skipped (dedupe)", jr.ID)
			continue
		}
		log.Printf("%s -> %v", jr.ID, jr.Value)
	}
	log.Printf("Stats: %+v", r.Stats())
}
