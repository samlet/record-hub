package observability

import (
	"strings"
	"testing"
	"time"
)

func TestQueryBudgetDefaultsAndValidation(t *testing.T) {
	budget := DefaultQueryBudget()
	if err := budget.Validate(); err != nil {
		t.Fatal(err)
	}
	if budget.MaxPageRows != 100 || budget.MaxResponseBytes != 1<<20 || budget.MaxDuration != 2*time.Second {
		t.Fatalf("unexpected defaults: %+v", budget)
	}
	for name, invalid := range map[string]QueryBudget{
		"rows":     {MaxPageRows: 0, MaxResponseBytes: 1 << 20, MaxDuration: time.Second},
		"bytes":    {MaxPageRows: 100, MaxResponseBytes: 1, MaxDuration: time.Second},
		"duration": {MaxPageRows: 100, MaxResponseBytes: 1 << 20, MaxDuration: time.Millisecond},
	} {
		if err := invalid.Validate(); err == nil || strings.TrimSpace(err.Error()) == "" {
			t.Fatalf("%s budget should be rejected", name)
		}
	}
}
