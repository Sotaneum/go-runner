// SPDX-License-Identifier: MIT

// Stats 모니터링 패턴: PushAndAwait으로 즉시 실행하면서 실시간 운영 지표를 출력한다.
package main

import (
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
	r := runner.New(3, runner.WithBatchBuffer(8))
	defer r.StopAndWait()

	// Stats를 주기적으로 출력하는 백그라운드 reporter
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				s := r.Stats()
				log.Printf("inFlight=%d batchDepth=%d/%d skips=%d dropped=%d",
					s.InFlightJobs, s.BatchQueueDepth, s.BatchQueueCapacity,
					s.DedupeSkipsTotal, s.DroppedResultsTotal)
			}
		}
	}()

	// 10개 Job을 즉시 실행 (limit=3이라 동시 3개씩 처리)
	jobs := make([]runner.JobInterface, 10)
	for i := range jobs {
		jobs[i] = &Job{id: "j", sleep: 100 * time.Millisecond}
	}
	res := <-r.PushAndAwait(jobs)
	close(stop)

	log.Printf("done in %v, items=%d", res.EndedAt.Sub(res.StartedAt), len(res.Items))
}
