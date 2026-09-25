package scheduler

import (
	"fmt"
	"sort"
)

// FieldError pinpoints one invalid field in the request.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// Validate checks structural and referential integrity of the request and
// returns every problem found, with JSON-path-like field locations.
func Validate(req *Request) []FieldError {
	var errs []FieldError
	bad := func(field, format string, args ...any) {
		errs = append(errs, FieldError{Field: field, Message: fmt.Sprintf(format, args...)})
	}

	if req.CrewSize < 1 {
		bad("crew_size", "must be >= 1")
	}
	if req.Horizon < 1 || req.Horizon > maxHorizon {
		bad("horizon", "must be between 1 and %d", maxHorizon)
	}
	if req.BudgetMS < 0 {
		bad("budget_ms", "must be >= 0")
	}
	if len(req.Operations) > maxOperations {
		bad("operations", "at most %d operations allowed", maxOperations)
	}

	eqIDs := map[string]bool{}
	for i, eq := range req.Equipment {
		field := fmt.Sprintf("equipment[%d]", i)
		if eq.ID == "" {
			bad(field+".id", "must not be empty")
		} else if eqIDs[eq.ID] {
			bad(field+".id", "duplicate equipment id %q", eq.ID)
		} else {
			eqIDs[eq.ID] = true
		}
		for j, win := range eq.Downtime {
			wf := fmt.Sprintf("%s.downtime[%d]", field, j)
			if win.Start < 0 {
				bad(wf+".start", "must be >= 0")
			}
			if win.End <= win.Start {
				bad(wf+".end", "must be greater than start")
			}
		}
	}

	opIDs := map[string]int{}
	for i, op := range req.Operations {
		field := fmt.Sprintf("operations[%d]", i)
		if op.ID == "" {
			bad(field+".id", "must not be empty")
		} else if _, dup := opIDs[op.ID]; dup {
			bad(field+".id", "duplicate operation id %q", op.ID)
		} else {
			opIDs[op.ID] = i
		}
		if op.Equipment == "" {
			bad(field+".equipment", "must not be empty")
		} else if !eqIDs[op.Equipment] {
			bad(field+".equipment", "unknown equipment %q", op.Equipment)
		}
		if op.Duration < 1 {
			bad(field+".duration", "must be >= 1")
		}
		if op.Workers < 1 {
			bad(field+".workers", "must be >= 1")
		} else if req.CrewSize >= 1 && op.Workers > req.CrewSize {
			bad(field+".workers", "exceeds crew_size")
		}
		if op.EarliestStart < 0 {
			bad(field+".earliest_start", "must be >= 0")
		}
		for j, dep := range op.Dependencies {
			df := fmt.Sprintf("%s.dependencies[%d]", field, j)
			if dep == op.ID && op.ID != "" {
				bad(df, "operation %q cannot depend on itself", dep)
				continue
			}
			if _, ok := opIDs[dep]; !ok {
				bad(df, "unknown operation id %q", dep)
			}
		}
	}

	if cycle := findCycle(req); cycle != nil {
		bad("operations", "dependency cycle detected: %s", fmt.Sprintf("%v", cycle))
	}
	return errs
}

// findCycle returns one dependency cycle as a list of operation IDs, or nil.
func findCycle(req *Request) []string {
	const (
		white, gray, black = 0, 1, 2
	)
	color := map[string]int{}
	deps := map[string][]string{}
	for _, op := range req.Operations {
		deps[op.ID] = op.Dependencies
	}
	var stack []string
	var cycle []string
	var visit func(id string) bool
	visit = func(id string) bool {
		color[id] = gray
		stack = append(stack, id)
		for _, dep := range deps[id] {
			switch color[dep] {
			case gray:
				for i, s := range stack {
					if s == dep {
						cycle = append([]string(nil), stack[i:]...)
					}
				}
				return true
			case white:
				if visit(dep) {
					return true
				}
			}
		}
		stack = stack[:len(stack)-1]
		color[id] = black
		return false
	}
	ids := make([]string, 0, len(req.Operations))
	for _, op := range req.Operations {
		ids = append(ids, op.ID)
	}
	sort.Strings(ids)
	for _, id := range ids {
		if color[id] == white && visit(id) {
			return cycle
		}
	}
	return nil
}
