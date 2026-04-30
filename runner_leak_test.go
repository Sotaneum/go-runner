// SPDX-License-Identifier: MIT

package runner_test

import (
	"testing"

	"go.uber.org/goleak"
)

// TestMain : 테스트 종료 시점에 살아있는 고루틴이 있으면 실패시킨다.
// Stop 누락이나 라이프사이클 누수를 자동으로 잡아낸다.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}
