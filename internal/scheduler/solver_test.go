package scheduler

import (
	"context"
	"testing"
)

func baseRequest() *Request {
	return &Request{
		Equipment: []Equipment{{ID: "E1"}, {ID: "E2"}},
		CrewSize:  2,
		Horizon:   480,
		BudgetMS:  2000,
	}
}

func op(id, eq string, dur, workers, es int, deps ...string) Operation {
	return Operation{ID: id, Equipment: eq, Duration: dur, Workers: workers, EarliestStart: es, Dependencies: deps}
}

func startsByID(res *Result) map[string]int {
	out := map[string]int{}
	for _, s := range res.Schedule {
		out[s.ID] = s.Start
	}
	return out
}

func TestOptimalRespectsDependenciesAndEquipment(t *testing.T) {
	req := baseRequest()
	req.CrewSize = 3
	req.Operations = []Operation{
		op("A", "E1", 10, 1, 0),
		op("B", "E1", 10, 1, 0, "A"),
		op("C", "E2", 15, 2, 5),
	}
	res := Solve(context.Background(), req)
	if res.Status != StatusOptimal {
		t.Fatalf("status=%s", res.Status)
	}
	if res.Makespan != 20 {
		t.Fatalf("makespan=%d want 20", res.Makespan)
	}
	starts := startsByID(res)
	if starts["A"] != 0 || starts["B"] != 10 || starts["C"] != 5 {
		t.Fatalf("starts=%v", starts)
	}
}

func TestDowntimeForcesWait(t *testing.T) {
	req := baseRequest()
	req.Equipment = []Equipment{{ID: "E1", Downtime: []DownInterval{{Start: 5, End: 20}}}}
	req.Operations = []Operation{op("A", "E1", 10, 1, 0)}
	res := Solve(context.Background(), req)
	if res.Status != StatusOptimal || res.Makespan != 30 {
		t.Fatalf("status=%s makespan=%d", res.Status, res.Makespan)
	}
	if got := startsByID(res)["A"]; got != 20 {
		t.Fatalf("A starts at %d, want 20 (must not cross downtime)", got)
	}
}

func TestActiveWaitBeatsGreedy(t *testing.T) {
	// Crew of 3. Greedy by ID order places A (2 workers) at 0, so B (2 workers)
	// must wait until 4 and C until 6: makespan 8. Delaying A lets B run at 0,
	// A and C overlap from 2, finishing at 6.
	req := baseRequest()
	req.CrewSize = 3
	req.Equipment = append(req.Equipment, Equipment{ID: "E3"})
	req.Operations = []Operation{
		op("A", "E1", 4, 2, 0),
		op("B", "E2", 2, 2, 0),
		op("C", "E3", 2, 1, 0, "B"),
	}
	res := Solve(context.Background(), req)
	if res.Status != StatusOptimal {
		t.Fatalf("status=%s", res.Status)
	}
	if res.Makespan != 6 {
		t.Fatalf("makespan=%d want 6 (active wait must beat greedy 8)", res.Makespan)
	}
}

func TestLexicographicTieBreak(t *testing.T) {
	// Same equipment, crew 1: orders (A@0,B@5) and (A@5,B@0) both finish at
	// 10; the ID-sorted start vector (0,5) must win.
	req := baseRequest()
	req.CrewSize = 1
	req.Operations = []Operation{
		op("A", "E1", 5, 1, 0),
		op("B", "E1", 5, 1, 0),
	}
	res := Solve(context.Background(), req)
	if res.Status != StatusOptimal || res.Makespan != 10 {
		t.Fatalf("status=%s makespan=%d", res.Status, res.Makespan)
	}
	starts := startsByID(res)
	if starts["A"] != 0 || starts["B"] != 5 {
		t.Fatalf("starts=%v want A=0 B=5", starts)
	}
}

func TestInfeasible(t *testing.T) {
	req := baseRequest()
	req.Operations = []Operation{op("A", "E1", 10, 3, 0)}
	res := Solve(context.Background(), req)
	if res.Status != StatusInfeasible {
		t.Fatalf("status=%s want infeasible (workers exceed crew)", res.Status)
	}
	if len(res.Schedule) != 0 {
		t.Fatalf("infeasible result must not carry a schedule")
	}
}

func TestTimeoutUnknown(t *testing.T) {
	// Equipment E1 alternates 30 free / 30 down minutes, leaving eight 30-min
	// slots for twelve 30-min operations: infeasible, but proving it requires
	// exploring all slot assignments. A 1ms budget must report timeout_unknown.
	req := baseRequest()
	req.CrewSize = 12
	req.BudgetMS = 1
	var downtime []DownInterval
	for start := 30; start < 480; start += 60 {
		downtime = append(downtime, DownInterval{Start: start, End: start + 30})
	}
	req.Equipment = []Equipment{{ID: "E1", Downtime: downtime}}
	for i := 0; i < 12; i++ {
		req.Operations = append(req.Operations, op(string(rune('A'+i)), "E1", 30, 1, 0))
	}
	res := Solve(context.Background(), req)
	if res.Status != StatusTimeoutUnknown {
		t.Fatalf("status=%s want timeout_unknown", res.Status)
	}
	if len(res.Schedule) != 0 {
		t.Fatalf("timeout_unknown must not carry a partial schedule")
	}
}

func TestTimeoutFeasible(t *testing.T) {
	// Twelve 40-min operations on one worker exactly fill the 480-min horizon.
	// A first complete plan is found immediately, but proving optimality means
	// exploring all permutations, so a 1ms budget yields timeout_feasible.
	req := baseRequest()
	req.CrewSize = 1
	req.BudgetMS = 1
	req.Equipment = nil
	for i := 0; i < 12; i++ {
		id := string(rune('A' + i))
		req.Equipment = append(req.Equipment, Equipment{ID: id})
		req.Operations = append(req.Operations, op(id, id, 40, 1, 0))
	}
	res := Solve(context.Background(), req)
	if res.Status != StatusTimeoutFeas {
		t.Fatalf("status=%s want timeout_feasible", res.Status)
	}
	if len(res.Schedule) != 12 || res.Makespan != 480 {
		t.Fatalf("expected complete 12-op plan with makespan 480, got %+v", res)
	}
}

func TestCanceled(t *testing.T) {
	req := baseRequest()
	req.Operations = []Operation{op("A", "E1", 5, 1, 0)}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := Solve(ctx, req)
	if res.Status != StatusCanceled {
		t.Fatalf("status=%s want canceled", res.Status)
	}
}
