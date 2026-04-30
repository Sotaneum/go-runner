// SPDX-License-Identifier: MIT

package runner

import (
	"errors"
	"testing"
)

func TestResultHelpers(t *testing.T) {
	pe := &PanicError{ID: "p", Recovered: "boom"}
	res := Result{Items: []JobResult{
		{ID: "ok", Value: 1},
		{ID: "err", Err: errors.New("x")},
		{ID: "p", Err: pe},
	}}
	by := res.ByID()
	if by["ok"].Value != 1 {
		t.Errorf("ByID[ok].Value = %v, want 1", by["ok"].Value)
	}
	if errs := res.Errors(); len(errs) != 2 || errs["err"] == nil || errs["p"] == nil {
		t.Errorf("Errors() = %v", errs)
	}
	if pes := res.PanicErrors(); len(pes) != 1 || pes["p"] != pe {
		t.Errorf("PanicErrors() = %v", pes)
	}
}

func TestSetResultDropsOldestOnOverflow(t *testing.T) {
	r := &Runner{
		ResultCh: make(chan Result, 2),
	}
	// 버퍼 크기 2 — 3번째부터 가장 오래된 것이 드롭됨
	r.setResult(Result{Items: []JobResult{{ID: "a"}}})
	r.setResult(Result{Items: []JobResult{{ID: "b"}}})
	r.setResult(Result{Items: []JobResult{{ID: "c"}}})
	if r.droppedResults.Load() != 1 {
		t.Errorf("droppedResults = %d, want 1", r.droppedResults.Load())
	}
	got := []string{}
	for len(got) < 2 {
		select {
		case res := <-r.ResultCh:
			got = append(got, res.Items[0].ID)
		default:
			t.Fatalf("결과 부족: %v", got)
		}
	}
	if got[0] != "b" || got[1] != "c" {
		t.Errorf("남은 결과 = %v, want [b, c]", got)
	}
}
