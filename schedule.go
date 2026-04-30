// SPDX-License-Identifier: MIT

package runner

import (
	"time"

	ktime "github.com/Sotaneum/go-kst-time"
)

// createQueue : 매 분 tick마다 batches에서 envelope 1개를 pop하고
// IsRun(now) 평가 후 queueCh로 전달. 없으면 그 tick은 스킵한다.
// 종료 시 남아있는 envelope들을 drain하여 PushAndAwait reply 채널을 닫고 leak을 방지한다.
func (r *Runner) createQueue() {
	defer r.drainBatches()
	for {
		select {
		case <-r.done:
			return
		case <-r.waitCh:
		}
		var env batchEnvelope
		select {
		case <-r.done:
			return
		case env = <-r.batches:
		default:
			continue
		}
		now := ktime.GetNow()
		filtered := make([]JobInterface, 0, len(env.jobs))
		for _, item := range env.jobs {
			if item.IsRun(now) {
				filtered = append(filtered, item)
			}
		}
		env.jobs = filtered
		select {
		case <-r.done:
			return
		case r.queueCh <- env:
		}
	}
}

// drainBatches : Stop 후 batches에 남은 envelope들을 비운다.
// PushAndAwait reply 채널이 있으면 닫아 호출자가 무한 대기하지 않게 한다.
func (r *Runner) drainBatches() {
	for {
		select {
		case env := <-r.batches:
			if env.replyCh != nil {
				close(env.replyCh)
			}
		default:
			return
		}
	}
}

// ingest : runnerCh로 들어오는 batch를 즉시 받아 batches로 전달.
// runnerCh가 닫히면 ingest가 종료되지만 다른 라이프사이클은 계속 살아있으므로
// 완전한 정리는 Stop()이 필요하다.
func (r *Runner) ingest() {
	for {
		select {
		case <-r.done:
			return
		case b, ok := <-r.runnerCh:
			if !ok {
				return
			}
			select {
			case <-r.done:
				return
			case r.batches <- batchEnvelope{jobs: b}:
			}
		}
	}
}

// timeChecker fires waitData every minute boundary, sleeping precisely to the
// next boundary to avoid drift. Returns when done is closed.
func timeChecker(waitData chan bool, done <-chan struct{}) {
	for {
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		select {
		case <-done:
			return
		case <-time.After(time.Until(next)):
		}
		select {
		case waitData <- true:
		case <-done:
			return
		default:
		}
	}
}
