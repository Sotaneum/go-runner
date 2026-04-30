// SPDX-License-Identifier: MIT

package runner

import (
	"fmt"
	"testing"
	"time"
)

type benchJob struct{ id string }

func (j *benchJob) GetID() string          { return j.id }
func (j *benchJob) IsRun(t time.Time) bool { return true }
func (j *benchJob) Run() (any, error)      { return nil, nil }

func benchRunner(b *testing.B, limit int, opts ...Option) *Runner {
	b.Helper()
	ch := make(chan []JobInterface, 1)
	r := NewRunnerWithLimit(ch, limit, opts...)
	b.Cleanup(r.StopAndWait)
	return r
}

func makeQueue(n int) []JobInterface {
	q := make([]JobInterface, n)
	for i := range q {
		q[i] = &benchJob{id: fmt.Sprintf("j%d", i)}
	}
	return q
}

func BenchmarkRunQueue_100Jobs(b *testing.B) {
	r := benchRunner(b, 50)
	q := makeQueue(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.queueCh <- batchEnvelope{jobs: q}
		<-r.ResultCh
	}
}

func BenchmarkRunQueue_1000Jobs(b *testing.B) {
	r := benchRunner(b, 50)
	q := makeQueue(1000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.queueCh <- batchEnvelope{jobs: q}
		<-r.ResultCh
	}
}

func BenchmarkRunQueue_Dedupe(b *testing.B) {
	r := benchRunner(b, 50, WithDedupe())
	q := makeQueue(100)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		r.queueCh <- batchEnvelope{jobs: q}
		<-r.ResultCh
	}
}
