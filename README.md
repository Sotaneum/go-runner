# go-runner

- Extract a list that needs to be run every minute and run a function.
- you can initialize and re-enter a Runner Object at any time.
- Runner object conforms to [`runner.JobInterface`](./runner.go#L10).
- The chain takes the form Runner[].

## Quick Start

### Download and install

```bash
$ go get -v github.com/Sotaneum/go-runner
```

### Build and run

```bash
$ go build runner.go
$ ./runner
```

### Testing

```bash
go test
```

- When the test ends, a `timeout panic` occurs.

  ```bash
  panic: test timed out after 10m0s
  ```

- Otherwise follow the `PASS > time` rule.

## Warning

- The latest version does not guarantee backward compatibility.
- `runner.ResultCh` must be empty to get the most recent log.

## MIT License

[Copyright (c) 2021 Sotaneum](./LICENSE)
