package scheduler

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
)

// bruteForce enumerates every start-time assignment to find the true optimal
// makespan and lexicographic start vector, for cross-checking the solver.
func bruteForce(req *Request) (int, []int, bool) {
	n := len(req.Operations)
	sorted := append([]Operation(nil), req.Operations...)
	// sort by ID for lexicographic comparison
	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			if sorted[j].ID < sorted[i].ID {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	idx := map[string]int{}
	for i, op := range sorted {
		idx[op.ID] = i
	}
	down := map[string][]DownInterval{}
	for _, eq := range req.Equipment {
		down[eq.ID] = eq.Downtime
	}
	best := req.Horizon + 1
	var bestStarts []int
	starts := make([]int, n)
	var rec func(k int)
	rec = func(k int) {
		if k == n {
			ms := 0
			for i, op := range sorted {
				if starts[i]+op.Duration > ms {
					ms = starts[i] + op.Duration
				}
			}
			if ms > best {
				return
			}
			if ms == best {
				for i := range starts {
					if starts[i] > bestStarts[i] {
						return
					}
					if starts[i] < bestStarts[i] {
						break
					}
				}
			}
			best = ms
			bestStarts = append([]int(nil), starts...)
			return
		}
		op := sorted[k]
		for t := op.EarliestStart; t+op.Duration <= req.Horizon; t++ {
			ok := true
			for _, win := range down[op.Equipment] {
				if win.Start < t+op.Duration && t < win.End {
					ok = false
				}
			}
			for _, dep := range op.Dependencies {
				d := idx[dep]
				if d >= k || starts[d]+sorted[d].Duration > t {
					ok = false
				}
			}
			for j := 0; j < k && ok; j++ {
				o := sorted[j]
				if o.Equipment == op.Equipment && starts[j] < t+op.Duration && t < starts[j]+o.Duration {
					ok = false
				}
			}
			if ok {
				for tt := t; tt < t+op.Duration && ok; tt++ {
					used := op.Workers
					for j := 0; j < k; j++ {
						if tt >= starts[j] && tt < starts[j]+sorted[j].Duration {
							used += sorted[j].Workers
						}
					}
					if used > req.CrewSize {
						ok = false
					}
				}
			}
			if ok {
				starts[k] = t
				rec(k + 1)
			}
		}
	}
	rec(0)
	return best, bestStarts, best <= req.Horizon
}

func TestSolverMatchesBruteForce(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 60; trial++ {
		nEq := 1 + rng.Intn(2)
		req := &Request{CrewSize: 1 + rng.Intn(3), Horizon: 18, BudgetMS: 60000}
		for e := 0; e < nEq; e++ {
			eq := Equipment{ID: fmt.Sprintf("E%d", e)}
			if rng.Intn(2) == 0 {
				s := rng.Intn(12)
				eq.Downtime = []DownInterval{{Start: s, End: s + 1 + rng.Intn(4)}}
			}
			req.Equipment = append(req.Equipment, eq)
		}
		n := 2 + rng.Intn(3)
		for i := 0; i < n; i++ {
			o := Operation{
				ID:            fmt.Sprintf("op%d", i),
				Equipment:     req.Equipment[rng.Intn(nEq)].ID,
				Duration:      2 + rng.Intn(4),
				Workers:       1 + rng.Intn(req.CrewSize),
				EarliestStart: rng.Intn(8),
			}
			if i > 0 && rng.Intn(2) == 0 {
				o.Dependencies = []string{fmt.Sprintf("op%d", rng.Intn(i))}
			}
			req.Operations = append(req.Operations, o)
		}
		wantMS, wantStarts, feasible := bruteForce(req)
		res := Solve(context.Background(), req)
		if !feasible {
			if res.Status != StatusInfeasible {
				t.Fatalf("trial %d: got %s, want infeasible\nreq=%+v", trial, res.Status, req)
			}
			continue
		}
		if res.Status != StatusOptimal || res.Makespan != wantMS {
			t.Fatalf("trial %d: got %s ms=%d, want optimal ms=%d\nreq=%+v", trial, res.Status, res.Makespan, wantMS, req)
		}
		got := startsByID(res)
		for i, op := range bruteSortedOps(req) {
			if got[op.ID] != wantStarts[i] {
				t.Fatalf("trial %d: op %s start=%d want %d\nreq=%+v", trial, op.ID, got[op.ID], wantStarts[i], req)
			}
		}
	}
}

func bruteSortedOps(req *Request) []Operation {
	sorted := append([]Operation(nil), req.Operations...)
	for i := 0; i < len(sorted); i++ {
		for j := i + 1; j < len(sorted); j++ {
			if sorted[j].ID < sorted[i].ID {
				sorted[i], sorted[j] = sorted[j], sorted[i]
			}
		}
	}
	return sorted
}
