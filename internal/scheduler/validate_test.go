package scheduler

import (
	"strings"
	"testing"
)

func expectField(t *testing.T, errs []FieldError, substr string) {
	t.Helper()
	for _, e := range errs {
		if strings.Contains(e.Field, substr) {
			return
		}
	}
	t.Fatalf("no error for field containing %q: %v", substr, errs)
}

func TestValidateOK(t *testing.T) {
	req := baseRequest()
	req.Operations = []Operation{op("A", "E1", 10, 1, 0), op("B", "E2", 10, 1, 0, "A")}
	if errs := Validate(req); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestValidateBadValues(t *testing.T) {
	req := baseRequest()
	req.CrewSize = 0
	req.Horizon = 481
	req.BudgetMS = -1
	req.Operations = []Operation{{ID: "A", Equipment: "E1", Duration: 0, Workers: 0, EarliestStart: -1}}
	errs := Validate(req)
	expectField(t, errs, "crew_size")
	expectField(t, errs, "horizon")
	expectField(t, errs, "budget_ms")
	expectField(t, errs, "operations[0].duration")
	expectField(t, errs, "operations[0].workers")
	expectField(t, errs, "operations[0].earliest_start")
}

func TestValidateDuplicatesAndUnknownRefs(t *testing.T) {
	req := baseRequest()
	req.Operations = []Operation{
		op("A", "E1", 5, 1, 0),
		op("A", "E9", 5, 1, 0, "ZZ"),
	}
	errs := Validate(req)
	expectField(t, errs, "operations[1].id")
	expectField(t, errs, "operations[1].equipment")
	expectField(t, errs, "operations[1].dependencies[0]")
}

func TestValidateCycle(t *testing.T) {
	req := baseRequest()
	req.Operations = []Operation{
		op("A", "E1", 5, 1, 0, "B"),
		op("B", "E1", 5, 1, 0, "A"),
	}
	errs := Validate(req)
	expectField(t, errs, "operations")
}

func TestValidateTooManyOps(t *testing.T) {
	req := baseRequest()
	for i := 0; i < 13; i++ {
		req.Operations = append(req.Operations, op(string(rune('A'+i)), "E1", 1, 1, 0))
	}
	expectField(t, Validate(req), "operations")
}
