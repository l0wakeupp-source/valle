package tools

import (
	"fmt"
	"sync/atomic"
)

var anonymousBudgetSequence atomic.Uint64

func nextAnonymousBudgetID() string {
	return fmt.Sprintf("anonymous-search-%d", anonymousBudgetSequence.Add(1))
}
