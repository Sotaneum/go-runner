# go-runner

> Job를 실행하고 그 결과를 반환합니다.

# Quick Start

## Download and install

```bash
$ go get -v github.com/Sotaneum/go-runner
```

## Create file `main.go`

```go
package main

import (
  "github.com/Sotaneum/go-runner"
)

type data struct {
  id string
}

func (d *data) GetID() string {
  return d.id
}

func (d *data) Run(param map[string]string) interface{} {
  fmt.Println("출력!")
  return "{code:200}"
}

func (d *data) IsRun() bool {
  return true
}

func main() {
  dataChan := make(chan []RunData)
  paramDataChan := make(chan map[string]string)

  run := runner.NewRunner(dataChan, paramDataChan)

  ... 작성중...
}
```

## Build and run

```bash
$ go build main.go
$ ./main
```

## Features

- 살짝 어렵습니다.
- 처리 결과를 반환합니다.
- 매 초마다 실행합니다.
- params 데이터를 통해서 Run 처리를 할 수 있습니다.

## MIT License

Copyright (c) 2021 Sotaneum
