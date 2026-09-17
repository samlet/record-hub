package observability

import (
	"errors"
	"fmt"
	"time"
)

var ErrQueryBudgetExceeded = errors.New("query cost budget exceeded")

// QueryBudget is the small, explicit cost envelope shared by record-list
// callers. It keeps page size, response size and execution time bounded even
// when a caller has valid workspace authorization.
type QueryBudget struct {
	MaxPageRows      int
	MaxResponseBytes int64
	MaxDuration      time.Duration
}

func DefaultQueryBudget() QueryBudget {
	return QueryBudget{MaxPageRows: 100, MaxResponseBytes: 1 << 20, MaxDuration: 2 * time.Second}
}

func (budget QueryBudget) Validate() error {
	if budget.MaxPageRows < 1 || budget.MaxPageRows > 1000 {
		return fmt.Errorf("query max page rows must be between 1 and 1000")
	}
	if budget.MaxResponseBytes < 1024 || budget.MaxResponseBytes > 16<<20 {
		return fmt.Errorf("query max response bytes must be between 1024 and 16777216")
	}
	if budget.MaxDuration < 10*time.Millisecond || budget.MaxDuration > 30*time.Second {
		return fmt.Errorf("query max duration must be between 10ms and 30s")
	}
	return nil
}
