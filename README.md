# go-runner

[![test](https://github.com/Sotaneum/go-runner/actions/workflows/test.yml/badge.svg)](https://github.com/Sotaneum/go-runner/actions/workflows/test.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/Sotaneum/go-runner.svg)](https://pkg.go.dev/github.com/Sotaneum/go-runner)
[![Go Report Card](https://goreportcard.com/badge/github.com/Sotaneum/go-runner)](https://goreportcard.com/report/github.com/Sotaneum/go-runner)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](./LICENSE)

A small Go library that runs a set of jobs every minute. Each minute the runner asks every registered job whether it should run (`IsRun(now)`), and concurrently executes the ones that say yes — up to a configurable concurrency limit.

- Push the **current set of jobs** to a channel; the runner queues batches internally and evaluates them on each minute tick.
- Jobs implement `JobInterface` (`IsRun`, `GetID`, `Run`).
- Concurrent execution within a tick, with a global concurrency cap.
- Backpressure-aware batch queue, optional dedupe, panic recovery, graceful shutdown, runtime stats.

## Install

```bash
go get github.com/Sotaneum/go-runner
```

## Quick Start

```go
package main

import (
    "errors"
    "log"
    "time"

    runner "github.com/Sotaneum/go-runner"
)

type Job struct{ id string }

func (j *Job) GetID() string          { return j.id }
func (j *Job) IsRun(t time.Time) bool { return true } // every minute
func (j *Job) Run() (any, error)      { return "done", nil }

func main() {
    runnerCh := make(chan []runner.JobInterface)
    r := runner.NewRunnerWithLimit(runnerCh, 10)
    defer r.StopAndWait()

    runnerCh <- []runner.JobInterface{&Job{id: "a"}, &Job{id: "b"}}

    for res := range r.ResultCh {
        for _, jr := range res.Items {
            switch {
            case errors.Is(jr.Err, runner.ErrSkippedDuplicate):
                log.Printf("%s skipped: duplicate in flight", jr.ID)
            case jr.Err != nil:
                log.Printf("%s failed: %v", jr.ID, jr.Err) // *runner.PanicError
            default:
                log.Printf("%s -> %v (%v)", jr.ID, jr.Value, jr.EndedAt.Sub(jr.StartedAt))
            }
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

- `runnerCh` accepts a **batch** (full job list) at a time. Each push is queued into the internal FIFO; the caller does not need to wait for the previous batch to finish.
- Every minute, `createQueue` pops one batch, evaluates `IsRun(now)` on each job, and forwards the surviving jobs to `start`.
- `start` hands the queue off to a background goroutine and immediately listens for the next queue, so a slow batch never blocks the next tick.
- Each job runs in its own goroutine, gated by a **global** concurrency semaphore (`limit`).
- Results for a queue are delivered as a single `Result` on `ResultCh` once all jobs in that queue finish.

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

Prevents the same `GetID()` from running concurrently. If a job with the same ID is already in flight (within the same queue or across overlapping queues), the duplicate is recorded as `JobResult{ID, Err: ErrSkippedDuplicate}` and `Run()` is not invoked. The skip is also reflected in `Stats().DedupeSkipsTotal`.

### `WithBatchBuffer(n)`

Sets the size of the internal FIFO queue that holds batches received from `runnerCh`. Default `64`, minimum `1`.

- Each minute tick consumes **one** batch.
- If the caller pushes batches faster than they are consumed, the queue fills and subsequent `runnerCh` sends **block**, providing natural backpressure. The caller does not need to manually pace itself.
- If `IsRun(now)` returns false at the time the batch is finally dequeued (e.g. it lagged behind), that job is dropped from the queue.

## Result and Errors

```go
type Result struct {
    StartedAt time.Time
    EndedAt   time.Time
    Items     []JobResult
}

type JobResult struct {
    ID        string
    Value     any       // Run() return value, valid only when Err == nil
    Err       error     // *PanicError, ErrSkippedDuplicate, or nil
    StartedAt time.Time
    EndedAt   time.Time
}

type PanicError struct {
    ID        string
    Recovered any
    Stack     []byte
}
```

Distinguish error sources with `errors.As` / `errors.Is`:

```go
var pe *runner.PanicError
switch {
case errors.As(jr.Err, &pe):
    log.Printf("%s panicked: %v\n%s", pe.ID, pe.Recovered, pe.Stack)
case errors.Is(jr.Err, runner.ErrSkippedDuplicate):
    // skipped by WithDedupe
case jr.Err != nil:
    // (none currently produced, reserved for future)
}
```

## Lifecycle

```go
r := runner.NewRunner(runnerCh)
defer r.StopAndWait() // graceful: stop accepting new work and wait for in-flight queues
```

| Method | Behavior |
|---|---|
| `Stop()` | Closes lifecycle goroutines (`start`, `createQueue`, `ingest`, `timeChecker`). In-flight `Run()` calls continue. Idempotent. |
| `Wait()` | Blocks until all in-flight `runQueue` goroutines complete. |
| `StopAndWait()` | `Stop()` followed by `Wait()`. |

In-flight `Run()` invocations are not cancelled — `JobInterface` does not currently expose a `context.Context`.

### Channel ownership

- The caller owns `runnerCh`. Closing it stops the `ingest` goroutine but does not stop the runner — call `Stop()` for that.
- The library owns `ResultCh`. It is buffered (size 8) and is closed by `Wait()` (or `StopAndWait`) once all in-flight queues have finished. After close, `range r.ResultCh` exits naturally.
- If the caller falls behind, the oldest result is dropped and `Stats().DroppedResultsTotal` is incremented.

## Stats

```go
s := r.Stats()
// s.InFlightJobs        — currently executing jobs (semaphore depth)
// s.BatchQueueDepth     — batches waiting to be consumed by createQueue
// s.BatchQueueCapacity  — configured via WithBatchBuffer
// s.DedupeSkipsTotal    — cumulative duplicate skips
// s.DroppedResultsTotal — cumulative ResultCh overflow drops
```

## API

| Function / Method | Description |
|---|---|
| `NewRunner(runnerCh)` | Create a runner with the default concurrency limit (50). |
| `NewRunnerWithLimit(runnerCh, limit, opts...)` | Create with custom limit and options. Panics if `runnerCh` is nil. `limit <= 0` normalizes to default. |
| `(*Runner).Stop()` | Stop lifecycle goroutines, wait for them to exit. Idempotent. |
| `(*Runner).Wait()` | Block until in-flight queues complete, then close `ResultCh`. |
| `(*Runner).StopAndWait()` | `Stop` + `Wait`. After this returns no goroutine from this runner remains. |
| `(*Runner).Stats()` | Snapshot of operational counters. |
| `(*Runner).InFlightIDs()` | Currently executing job IDs (only meaningful with `WithDedupe`). |
| `(*Runner).ResultCh` | Buffered receive channel of `Result`. |
| `WithDedupe()` | Skip duplicate in-flight IDs. |
| `WithBatchBuffer(n)` | Set internal batch FIFO size. |
| `ErrSkippedDuplicate` | Sentinel error for dedupe skips. |
| `PanicError` | Wraps a recovered `Run()` panic with `Recovered` and `Stack`. |

### `JobInterface`

```go
type JobInterface interface {
    IsRun(t time.Time) bool // called once per tick at evaluation time
    GetID() string          // stable identifier; result map key (and dedupe key if enabled)
    Run() (any, error)      // executed when IsRun returned true.
                            // value goes to JobResult.Value, error to JobResult.Err.
                            // a panic also lands in Err as *PanicError.
}
```

## Examples

Runnable programs in `examples/`:

- [`examples/basic`](./examples/basic) — minimal usage
- [`examples/dedupe`](./examples/dedupe) — `WithDedupe()` + `Stats`

## Testing

```bash
go test -race ./...               # unit + leak (via go.uber.org/goleak)
go test -bench=. -benchmem ./...  # benchmarks
staticcheck ./...                 # style/lint
```

## License

MIT — [Copyright (c) 2021 Sotaneum](./LICENSE)
