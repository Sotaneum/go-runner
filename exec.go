// SPDX-License-Identifier: MIT

package runner

import (
	"runtime/debug"
	"sync"
	"time"
)

// start consumes queueCh and spawns runQueue per envelope.
// 큐 단위로 runQueue를 즉시 분리하여 createQueue가 다음 분 tick에서 막히지 않게 한다.
func (r *Runner) start() {
	for {
		select {
		case <-r.done:
			return
		case env := <-r.queueCh:
			r.inFlightQueues.Add(1)
			go func(e batchEnvelope) {
				defer r.inFlightQueues.Done()
				r.runQueue(e)
			}(env)
		}
	}
}

// runQueue executes a single queue concurrently and emits the Result.
func (r *Runner) runQueue(env batchEnvelope) {
	startedAt := time.Now()
	items := make([]JobResult, 0, len(env.jobs))
	var (
		wg sync.WaitGroup
		mu sync.Mutex
	)

	appendResult := func(jr JobResult) {
		mu.Lock()
		items = append(items, jr)
		mu.Unlock()
	}

	for _, item := range env.jobs {
		if r.dedupe && !r.markInFlight(item.GetID()) {
			r.dedupeSkips.Add(1)
			if r.onSkip != nil {
				r.onSkip(item.GetID())
			}
			now := time.Now()
			appendResult(JobResult{
				ID:        item.GetID(),
				Err:       ErrSkippedDuplicate,
				StartedAt: now,
				EndedAt:   now,
			})
			continue
		}
		wg.Add(1)
		// 전역 세마포어: 슬롯이 빌 때까지 블로킹 대기.
		r.sem <- struct{}{}
		go func(j JobInterface) {
			defer wg.Done()
			defer func() { <-r.sem }()
			if r.dedupe {
				defer r.unmarkInFlight(j.GetID())
			}
			started := time.Now()
			var (
				out any
				err error
			)
			func() {
				// 라이브러리 레벨 recover. 패닉이 프로세스를 죽이지 않도록.
				defer func() {
					if p := recover(); p != nil {
						stack := debug.Stack()
						if r.onPanic != nil {
							r.onPanic(j.GetID(), p, stack)
						}
						out = nil
						err = &PanicError{
							ID:        j.GetID(),
							Recovered: p,
							Stack:     stack,
						}
					}
				}()
				out, err = j.Run()
			}()
			appendResult(JobResult{
				ID:        j.GetID(),
				Value:     out,
				Err:       err,
				StartedAt: started,
				EndedAt:   time.Now(),
			})
		}(item)
	}
	wg.Wait()

	res := Result{
		StartedAt: startedAt,
		EndedAt:   time.Now(),
		Items:     items,
	}
	r.setResult(res)
	if env.replyCh != nil {
		env.replyCh <- res
		close(env.replyCh)
	}
}

// markInFlight returns false if id is already in flight; otherwise registers it.
func (r *Runner) markInFlight(id string) bool {
	r.inFlightMu.Lock()
	defer r.inFlightMu.Unlock()
	if _, exists := r.inFlight[id]; exists {
		return false
	}
	r.inFlight[id] = struct{}{}
	return true
}

func (r *Runner) unmarkInFlight(id string) {
	r.inFlightMu.Lock()
	delete(r.inFlight, id)
	r.inFlightMu.Unlock()
}

// setResult delivers a Result on ResultCh, dropping the oldest if full.
// 가장 오래된 결과를 비워서 새 결과 자리를 만든다.
func (r *Runner) setResult(res Result) {
	for {
		select {
		case r.ResultCh <- res:
			return
		default:
			select {
			case <-r.ResultCh:
				r.droppedResults.Add(1)
			default:
			}
		}
	}
}
