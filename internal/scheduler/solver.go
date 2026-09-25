package scheduler

import (
	"context"
	"sort"
	"time"
)

// DownInterval is a half-open [Start, End) equipment downtime window.
type DownInterval struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

// Equipment describes one maintainable device and its downtime windows.
type Equipment struct {
	ID       string         `json:"id"`
	Downtime []DownInterval `json:"downtime"`
}

// Operation is one non-preemptive maintenance procedure.
type Operation struct {
	ID            string   `json:"id"`
	Equipment     string   `json:"equipment"`
	Duration      int      `json:"duration"`
	Workers       int      `json:"workers"`
	EarliestStart int      `json:"earliest_start"`
	Dependencies  []string `json:"dependencies"`
}

// Request is the scheduling problem.
type Request struct {
	Equipment  []Equipment `json:"equipment"`
	CrewSize   int         `json:"crew_size"`
	Horizon    int         `json:"horizon"`
	BudgetMS   int64       `json:"budget_ms"`
	Operations []Operation `json:"operations"`
}

// ScheduledOperation is one entry of a complete plan.
type ScheduledOperation struct {
	ID        string `json:"id"`
	Equipment string `json:"equipment"`
	Start     int    `json:"start"`
	End       int    `json:"end"`
	Workers   int    `json:"workers"`
}

// Result is the solver outcome. Schedule is only present when a complete
// feasible plan was found.
type Result struct {
	Status    string               `json:"status"`
	Schedule  []ScheduledOperation `json:"schedule,omitempty"`
	Makespan  int                  `json:"makespan,omitempty"`
	ElapsedMS int64                `json:"elapsed_ms"`
	Nodes     int64                `json:"nodes_explored"`
}

// Solver status values. See README for their exact meaning.
const (
	StatusOptimal        = "optimal"          // search exhausted; plan proven optimal
	StatusInfeasible     = "infeasible"       // search exhausted; no complete plan exists
	StatusTimeoutFeas    = "timeout_feasible" // budget exhausted; feasible but unproven plan returned
	StatusTimeoutUnknown = "timeout_unknown"  // budget exhausted before any complete plan was found
	StatusCanceled       = "canceled"         // request context was canceled
)

const maxOperations = 12
const maxHorizon = 480

type placed struct {
	opIndex int
	start   int
	end     int
}

type solver struct {
	req      *Request
	ops      []*Operation // indexed by ID-sorted rank
	downtime map[string][]DownInterval
	deps     [][]int
	events   []int // global candidate start times (see buildEvents)

	done      uint16
	placed    []*placed
	best      []int
	bestMS    int
	nodes     int64
	deadline  time.Time
	hasBudget bool
}

// Solve runs the branch-and-bound search. Every call creates an isolated
// solver, so plans and budgets never leak between requests.
func Solve(ctx context.Context, req *Request) *Result {
	start := time.Now()
	s := &solver{
		req:       req,
		downtime:  make(map[string][]DownInterval),
		best:      make([]int, len(req.Operations)),
		bestMS:    req.Horizon + 1,
		deadline:  start.Add(time.Duration(req.BudgetMS) * time.Millisecond),
		hasBudget: req.BudgetMS > 0,
	}
	order := make([]int, len(req.Operations))
	for i := range req.Operations {
		order[i] = i
	}
	sort.Slice(order, func(i, j int) bool {
		return req.Operations[order[i]].ID < req.Operations[order[j]].ID
	})
	s.ops = make([]*Operation, len(req.Operations))
	idToRank := make(map[string]int, len(req.Operations))
	for rank, idx := range order {
		s.ops[rank] = &req.Operations[idx]
		idToRank[req.Operations[idx].ID] = rank
	}
	s.deps = make([][]int, len(s.ops))
	for rank, op := range s.ops {
		seen := map[int]bool{}
		for _, depID := range op.Dependencies {
			d := idToRank[depID]
			if !seen[d] {
				s.deps[rank] = append(s.deps[rank], d)
				seen[d] = true
			}
		}
	}
	for _, eq := range req.Equipment {
		windows := append([]DownInterval(nil), eq.Downtime...)
		sort.Slice(windows, func(i, j int) bool { return windows[i].Start < windows[j].Start })
		s.downtime[eq.ID] = windows
	}
	s.events = s.buildEvents()
	if len(s.ops) == 0 {
		return &Result{Status: StatusOptimal, ElapsedMS: time.Since(start).Milliseconds()}
	}

	s.search(ctx)
	elapsed := time.Since(start)

	if ctx.Err() != nil {
		return &Result{Status: StatusCanceled, ElapsedMS: elapsed.Milliseconds(), Nodes: s.nodes}
	}
	if s.hasBudget && !time.Now().Before(s.deadline) {
		if s.bestMS <= s.req.Horizon {
			return &Result{Status: StatusTimeoutFeas, Schedule: s.buildSchedule(), Makespan: s.bestMS,
				ElapsedMS: elapsed.Milliseconds(), Nodes: s.nodes}
		}
		return &Result{Status: StatusTimeoutUnknown, ElapsedMS: elapsed.Milliseconds(), Nodes: s.nodes}
	}
	if s.bestMS <= s.req.Horizon {
		return &Result{Status: StatusOptimal, Schedule: s.buildSchedule(), Makespan: s.bestMS,
			ElapsedMS: elapsed.Milliseconds(), Nodes: s.nodes}
	}
	return &Result{Status: StatusInfeasible, ElapsedMS: elapsed.Milliseconds(), Nodes: s.nodes}
}

func (s *solver) buildSchedule() []ScheduledOperation {
	out := make([]ScheduledOperation, len(s.ops))
	for rank, op := range s.ops {
		out[rank] = ScheduledOperation{
			ID:        op.ID,
			Equipment: op.Equipment,
			Start:     s.best[rank],
			End:       s.best[rank] + op.Duration,
			Workers:   op.Workers,
		}
	}
	return out
}

func (s *solver) stop(ctx context.Context) bool {
	if ctx.Err() != nil {
		return true
	}
	if s.hasBudget && s.nodes&1023 == 0 && !time.Now().Before(s.deadline) {
		return true
	}
	return false
}

// feasible reports whether op at rank can occupy half-open [start, end).
func (s *solver) feasible(rank, start int) bool {
	op := s.ops[rank]
	end := start + op.Duration
	if start < op.EarliestStart || end > s.req.Horizon {
		return false
	}
	for _, d := range s.deps[rank] {
		depEnd := -1
		for _, p := range s.placed {
			if p.opIndex == d {
				depEnd = p.end
			}
		}
		if depEnd < 0 || start < depEnd {
			return false
		}
	}
	for _, win := range s.downtime[op.Equipment] {
		if win.End <= start {
			continue
		}
		if win.Start >= end {
			break
		}
		return false
	}
	for _, p := range s.placed {
		other := s.ops[p.opIndex]
		if other.Equipment == op.Equipment && p.start < end && start < p.end {
			return false
		}
	}
	for t := start; t < end; t++ {
		used := op.Workers
		for _, p := range s.placed {
			if t >= p.start && t < p.end {
				used += s.ops[p.opIndex].Workers
			}
		}
		if used > s.req.CrewSize {
			return false
		}
	}
	return true
}

// buildEvents computes the closure of all times at which any operation could
// ever start: earliest starts and downtime ends, plus every operation end
// reachable by chaining durations from those seeds. In an optimal schedule
// every operation starts either at its earliest start, at a downtime end, or
// exactly when another operation ends (otherwise it could start earlier), so
// this set is complete. The closure is capped by the horizon.
func (s *solver) buildEvents() []int {
	in := make([]bool, s.req.Horizon+1)
	var queue []int
	add := func(t int) {
		if t >= 0 && t <= s.req.Horizon && !in[t] {
			in[t] = true
			queue = append(queue, t)
		}
	}
	for _, op := range s.ops {
		add(op.EarliestStart)
	}
	for _, windows := range s.downtime {
		for _, win := range windows {
			add(win.End)
		}
	}
	for len(queue) > 0 {
		t := queue[0]
		queue = queue[1:]
		for _, op := range s.ops {
			add(t + op.Duration)
		}
	}
	out := make([]int, 0, s.req.Horizon+1)
	for t, ok := range in {
		if ok {
			out = append(out, t)
		}
	}
	sort.Ints(out)
	return out
}

// candidates returns the feasible start times for the op, drawn from the
// precomputed event set, in ascending order.
func (s *solver) candidates(rank int) []int {
	op := s.ops[rank]
	out := make([]int, 0, 16)
	for _, t := range s.events {
		if t+op.Duration > s.req.Horizon {
			break
		}
		if t < op.EarliestStart {
			continue
		}
		if s.feasible(rank, t) {
			out = append(out, t)
		}
	}
	return out
}

// lowerBound is a resource-free critical-path estimate: the earliest
// possible completion of every remaining operation ignoring device and crew
// contention. Pruning is therefore safe.
func (s *solver) lowerBound() int {
	n := len(s.ops)
	ef := make([]int, n)
	for _, p := range s.placed {
		ef[p.opIndex] = p.end
	}
	changed := true
	for changed {
		changed = false
		for rank, op := range s.ops {
			if s.done&(1<<rank) != 0 {
				continue
			}
			es := op.EarliestStart
			for _, d := range s.deps[rank] {
				if ef[d] > es {
					es = ef[d]
				}
			}
			if v := es + op.Duration; v != ef[rank] {
				ef[rank] = v
				changed = true
			}
		}
	}
	lb := 0
	for _, v := range ef {
		if v > lb {
			lb = v
		}
	}
	return lb
}

// search returns false when the search must stop (budget/cancellation).
func (s *solver) search(ctx context.Context) bool {
	s.nodes++
	if s.stop(ctx) {
		return false
	}
	if int(s.done) == 1<<len(s.ops)-1 {
		ms := 0
		for _, p := range s.placed {
			if p.end > ms {
				ms = p.end
			}
		}
		s.record(ms)
		return true
	}
	if s.lowerBound() > s.bestMS {
		return true
	}
	rank, choices := s.pickBranch()
	if len(choices) == 0 {
		return true
	}
	for _, start := range choices {
		p := &placed{opIndex: rank, start: start, end: start + s.ops[rank].Duration}
		s.placed = append(s.placed, p)
		s.done |= 1 << rank
		alive := s.search(ctx)
		s.done &^= 1 << rank
		s.placed = s.placed[:len(s.placed)-1]
		if !alive {
			return false
		}
	}
	return true
}

// pickBranch chooses the schedulable unscheduled operation with the fewest
// start candidates (fail-first); IDs break ties deterministically.
func (s *solver) pickBranch() (int, []int) {
	bestRank := -1
	var bestChoices []int
	for rank := range s.ops {
		if s.done&(1<<rank) != 0 {
			continue
		}
		ready := true
		for _, d := range s.deps[rank] {
			if s.done&(1<<d) == 0 {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		choices := s.candidates(rank)
		if bestRank == -1 || len(choices) < len(bestChoices) {
			bestRank, bestChoices = rank, choices
		}
	}
	return bestRank, bestChoices
}

// record keeps the plan with smallest makespan; ties are resolved by the
// lexicographically smallest start-time vector over IDs sorted ascending.
func (s *solver) record(makespan int) {
	starts := make([]int, len(s.ops))
	for _, p := range s.placed {
		starts[p.opIndex] = p.start
	}
	if makespan > s.bestMS {
		return
	}
	if makespan == s.bestMS {
		for i := range starts {
			if starts[i] > s.best[i] {
				return
			}
			if starts[i] < s.best[i] {
				break
			}
		}
	}
	s.bestMS = makespan
	copy(s.best, starts)
}
