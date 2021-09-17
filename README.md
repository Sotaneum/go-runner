# go-runner

- 매 분마다 실행해야하는 목록을 추출하고 함수를 실행합니다.
- 실행을 검토하는 Runner 객체를 언제든지 초기화하고 새로 입력할 수 있습니다.
- Runner 객체는 `runner.RunnerInterface` 를 따릅니다.
- 체인은 Runner[] 형태를 받습니다.

# Quick Start

## Download and install

```bash
$ go get -v github.com/Sotaneum/go-runner
```

## Build and run

```bash
$ go build runner.go
$ ./runner
```

## Testing

```bash
go test
```

- 테스트는 테스트 `Timeout panic`이 발생할 때까지 동작합니다.

  ```bash
  panic: test timed out after 10m0s
  ```

- 그 외의 경우에는 `PASS > time` 규칙을 따릅니다.

## Features

- 매 분마다 `IsRun` 함수를 바탕으로 `queue 목록`을 생성하고 각 `Runner`의 `Run` 함수를 실행합니다.
- 처리 결과(Run 함수의 반환 값)를 `runner.ResultCh` 으로 받을 수 있습니다. (값을 받지 않더라도 체인을 비워두는 것을 권장합니다.)

## MIT License

Copyright (c) 2021 Sotaneum
