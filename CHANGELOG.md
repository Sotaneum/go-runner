# Changelog

All notable changes are documented here. This project follows [Semantic Versioning](https://semver.org/).

## [0.1.0-beta]

Breaking changes from the original (pre-versioned) library. A `v0.1.0` tag is recommended once cut.

### Fixed

- **`PushAndAwait` 데드락 방지.** RLock을 보유한 채 `queueCh`로 무제한 블록 송신하던 부분을 수정. stopped 플래그 확인 후 락을 즉시 해제하고, 송신은 `r.done`과 함께 select하여 Stop과 동시 호출되어도 영구 대기하지 않는다.
- **세마포어 획득의 Stop 무반응.** 동시성 한도가 가득 찬 상태에서 `Stop`이 호출되면 슬롯을 기다리지 않고 종료한다. 시작되지 못한 잡은 `JobResult{Err: ErrShuttingDown}`로 결과에 기록된다.

### Added

- **`ErrShuttingDown` sentinel.** Stop으로 인해 시작되지 못한 잡의 `JobResult.Err`로 보고된다.

### Breaking

- **Concurrent execution.** Jobs within a queue now run concurrently, not serially. Callers whose `Run()` is not safe under concurrent invocation must serialize externally.
- **Global concurrency limit.** Default limit is `50` (`defaultConcurrencyLimit`). Use `NewRunnerWithLimit(ch, n)` to override.
- **`ResultCh` type changed** from `chan map[string]interface{}` to `chan Result`. `Result` carries `[]JobResult` with per-job timing and explicit error fields. Migration:
  ```go
  // before
  for id, v := range <-r.ResultCh { ... }
  // after
  for _, jr := range (<-r.ResultCh).Items {
      if jr.Err != nil { /* PanicError or ErrSkippedDuplicate */ continue }
      _ = jr.Value
  }
  ```
- **`JobInterface.Run()` signature changed** from `Run() interface{}` to `Run() (any, error)`. A returned error lands in `JobResult.Err`. A panic still lands in `Err` as `*PanicError` and overrides any value/error returned.
- **`runnerCh` may not be nil.** `NewRunnerWithLimit(nil, …)` panics.
- **`Stop()` is synchronous.** It now waits for lifecycle goroutines to exit before returning. `Wait()` waits for in-flight queues and closes `ResultCh`. After `StopAndWait` returns, no goroutine from the runner remains.

### Added

- **Panic recovery.** A `Run()` panic is captured into `JobResult.Err` as a `*PanicError` (with `Recovered`, `Stack`). Other jobs in the same queue continue. The runner stays alive.
- **`Stop()`, `Wait()`, `StopAndWait()`.** Lifecycle goroutines can now be cleanly torn down. `Wait` blocks until in-flight `runQueue`s complete.
- **`Stats()`.** Returns a snapshot of in-flight job count, batch queue depth, dedupe skip total, and dropped result total.
- **`WithDedupe()` option.** Skips a job whose `GetID()` is already in flight; the duplicate is recorded as `JobResult{Err: ErrSkippedDuplicate}`.
- **`WithBatchBuffer(n)` option.** Internal FIFO queue size for incoming batches (default 64). Senders to `runnerCh` block when full, providing natural backpressure.
- **Variadic option pattern.** `NewRunnerWithLimit(ch, limit, opts...)` is source-compatible with existing 2-arg callers.
- **`InFlightIDs()` method.** Returns currently executing job IDs (meaningful only with `WithDedupe`).
- **`ResultCh` auto-close on `Wait()` / `StopAndWait()`.** Enables `for res := range r.ResultCh` to terminate naturally.
- **`doc.go`** with package overview.
- **Testable `Example_*` functions** so godoc / pkg.go.dev can render runnable examples.
- **Benchmarks** (`runner_bench_test.go`).
- **Goroutine leak detection** in tests via `go.uber.org/goleak`.
- **`examples/`** directory with runnable programs (`basic`, `dedupe`).
- **CHANGELOG and CI workflow** (vet, staticcheck, build, race tests, coverage).
- **`New(limit, opts...)`** constructor with an internally-managed batch channel.
- **`Push(batch)`** — submit a batch without managing the channel directly.
- **`PushAndAwait(batch)`** — synchronous, immediate execution with a per-batch reply channel.
- **`WithOnPanic(fn)`** and **`WithOnSkip(fn)`** option callbacks.
- **`Result.ByID()`, `Result.Errors()`, `Result.PanicErrors()`** convenience helpers.

- **`PanicError.Unwrap()`.** When a job panics with an `error` value (e.g. `panic(io.EOF)`), `errors.Is(jr.Err, io.EOF)` works against the recovered value.
- **Stop drains pending batches.** Envelopes left in the internal FIFO when `Stop` runs are drained; any `PushAndAwait` reply channels are closed so callers don't block.
- **`Push` returns `bool`.** `false` indicates the runner has been stopped and the batch was discarded (no longer hangs on a closed pipeline).
- **`PushAndAwait` is shutdown-aware.** The send to the internal queue races against `done`, so `Stop` can never deadlock against a `PushAndAwait` waiting on a busy queue. The reply channel is closed if shutdown wins.
- **`ErrShuttingDown`.** Reported in `JobResult.Err` for jobs that had been admitted to a queue but were still waiting for a concurrency slot when `Stop` fired. They are not executed; instead each pending job receives this sentinel so the caller sees a complete `Result` rather than a hang.
- **`examples/with-stats`** demonstrating `Stats()` monitoring + `PushAndAwait`.

### File layout

The package is split into focused files for readability:

- `types.go` — public types, errors, constants, and the internal `batchEnvelope`
- `options.go` — `Option` and `With*` functions
- `runner.go` — `Runner` struct, lifecycle (`Stop`/`Wait`/`StopAndWait`), `Stats`, `InFlightIDs`, constructors, `Push`, `PushAndAwait`
- `exec.go` — `start`, `runQueue`, dedupe helpers, `setResult`
- `schedule.go` — `createQueue`, `ingest`, `timeChecker`

### Changed

- **Tick scheduling.** `timeChecker` now sleeps until the next minute boundary (`time.Until(next)`) instead of polling every second, eliminating drift.
- **Batch queueing.** `dispatchRunner` is gone; an `ingest` goroutine forwards batches into an internal FIFO immediately. The 1-second polling delay is removed.
- **`ResultCh` is buffered (size 8).** When the buffer overflows, the oldest result is dropped and `Stats().DroppedResultsTotal` is incremented.
- **Receiver name** standardized to `r` across methods.

### Removed

- `dispatchRunner` and `nextCh` (replaced by `ingest` + `batches`).
- The infinite-loop `TestRunner` that relied on a 10-minute timeout panic.
