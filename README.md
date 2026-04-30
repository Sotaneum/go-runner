# go-runner

A small Go library that runs a set of jobs every minute. Each minute the runner asks every registered job whether it should run (`IsRun(now)`), and concurrently executes the ones that say yes — up to a configurable concurrency limit.

- Push the **current set of jobs** to a channel; the runner snapshots and evaluates them on each minute tick.
- Jobs implement `JobInterface` (`IsRun`, `GetID`, `Run`).
- Concurrent execution within a tick, with a global concurrency cap.
- Backpressure-aware batch queue, optional dedupe, panic recovery, graceful shutdown.

## Install

```bash
go get github.com/Sotaneum/go-runner
```

## Quick Start

```go
package main

import (
    "log"
    "time"

    runner "github.com/Sotaneum/go-runner"
)

type Job struct{ id string }

func (j *Job) GetID() string         { return j.id }
func (j *Job) IsRun(t time.Time) bool { return true } // run every minute
func (j *Job) Run() interface{}       { return "done" }

func main() {
    runnerCh := make(chan []runner.JobInterface)
    r := runner.NewRunnerWithLimit(runnerCh, 10)
    defer r.Stop()

    runnerCh <- []runner.JobInterface{&Job{id: "a"}, &Job{id: "b"}}

    for result := range r.ResultCh {
        for id, v := range result {
            if err, ok := v.(error); ok {
                log.Printf("job %s panicked: %v", id, err)
                continue
            }
            log.Printf("job %s -> %v", id, v)
        }
    }
}
```

## How It Works

```
caller ──[runnerCh]──▶ ingest ──▶ batch FIFO ──▶ createQueue ──▶ start ──▶ runQueue
                                       ▲              │           │
                              minute tick (timeChecker)            └─▶ goroutine per job (capped by limit) ──▶ ResultCh
```

- `runnerCh` accepts a **batch** (full job list) at a time. Each push is queued internally; the caller does not need to wait for the previous batch to finish.
- Every minute, `createQueue` pops one batch, evaluates `IsRun(now)` on each job, and forwards the surviving jobs to `start`.
- `start` hands the queue off to a background goroutine and immediately listens for the next queue, so a slow batch never blocks the next tick.
- Each job runs in its own goroutine, gated by a **global** concurrency semaphore (`limit`).
- Results for a queue are delivered as a single `map[string]interface{}` on `ResultCh` once all jobs in that queue finish.

## Concurrency

- Jobs in a single queue execute concurrently.
- The concurrency limit is **global across all in-flight queues** (not per-queue).
- When the limit is reached, additional jobs **block** until a slot frees up.
- Default limit: `50`. Set explicitly via `NewRunnerWithLimit`.

> Because queues can overlap, the same `GetID()` appearing in two queues will, by default, run twice concurrently. Either make `Run()` safe for that, ensure uniqueness across queues, or enable [`WithDedupe()`](#withdedupe).

## Options

`NewRunnerWithLimit` accepts variadic `Option` values; existing callers without options remain source-compatible.

```go
r := runner.NewRunnerWithLimit(runnerCh, 10,
    runner.WithDedupe(),
    runner.WithBatchBuffer(64),
)
```

### `WithDedupe()`

Prevents the same `GetID()` from running concurrently. If a job with the same ID is already in flight (within the same queue or across overlapping queues), the duplicate is silently skipped and omitted from the result map.

### `WithBatchBuffer(n)`

Sets the size of the internal FIFO queue that holds batches received from `runnerCh`. Default `64`, minimum `1`.

- Each minute tick consumes **one** batch.
- If the caller pushes batches faster than they are consumed, the queue fills and subsequent `runnerCh` sends **block**, providing natural backpressure. The caller does not need to manually pace itself.
- If `IsRun(now)` returns false at the time the batch is finally dequeued (e.g. it lagged behind), that job is dropped from the queue.

## Panic Handling

If `Run()` panics, the library recovers it and stores an `error` value in the result map under the job's ID. Other jobs in the same queue continue to run; the runner stays alive.

```go
result := <-r.ResultCh
for id, v := range result {
    if err, ok := v.(error); ok {
        log.Printf("job %s failed: %v", id, err)
        continue
    }
    // v is the value returned by Run()
}
```

## Lifecycle

Call `Stop()` to terminate the runner's internal goroutines (`start`, `createQueue`, `ingest`, `timeChecker`). It is safe to call multiple times.

```go
r := runner.NewRunner(runnerCh)
defer r.Stop()
```

In-flight `Run()` calls are **not** cancelled (the current `JobInterface` exposes no context); they run to completion. `Stop()` only stops new work from being scheduled.

## API

| Function / Method | Description |
|---|---|
| `NewRunner(runnerCh)` | Create a runner with the default concurrency limit (50) and default options. |
| `NewRunnerWithLimit(runnerCh, limit, opts...)` | Create a runner with a custom limit and options. Panics if `runnerCh` is nil. `limit <= 0` is normalized to the default. |
| `(*Runner).Stop()` | Stop lifecycle goroutines. Idempotent. |
| `(*Runner).ResultCh` | Receive-only stream of per-queue result maps. Buffered; if the caller falls behind, oldest results are dropped. |
| `WithDedupe()` | Skip jobs whose `GetID()` is already in flight. |
| `WithBatchBuffer(n)` | Set internal batch FIFO size. |

### `JobInterface`

```go
type JobInterface interface {
    IsRun(t time.Time) bool // called once per tick at evaluation time
    GetID() string          // stable identifier; used as the result map key (and dedupe key if enabled)
    Run() interface{}       // executed when IsRun returned true; return value is stored in the result map
}
```

## Testing

```bash
go test -race ./...
```

## License

MIT — [Copyright (c) 2021 Sotaneum](./LICENSE)
