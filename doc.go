// SPDX-License-Identifier: MIT

// Package runner schedules and concurrently executes a set of jobs every minute.
//
// Each minute the runner asks every registered job whether it should run via
// JobInterface.IsRun(now), and concurrently executes the ones that say yes — up
// to a configurable global concurrency limit.
//
// The caller pushes the current set of jobs to a channel; batches are queued
// internally (FIFO with optional backpressure) and evaluated on each tick.
// A job's panic does not bring down the runner — it is recovered and reported
// as *PanicError on JobResult.Err.
//
// See the README and the Example_* functions for usage patterns.
package runner
