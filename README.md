# Shutdown Scheduler

Local-only maintenance scheduling service (Go 1.27.1 + Chi 5.2.1). Given
equipment downtime windows, a fixed crew and a set of maintenance operations,
it searches for the schedule with the earliest completion time. It never
touches real equipment or any external system.

## Running

```sh
go test ./...
go build -o bin/server ./cmd/server
go run ./cmd/server -addr 127.0.0.1:8080
```

## API

`GET /healthz` returns `{"status":"ok"}`.

`POST /v1/schedule` accepts JSON (integer minutes, measured from shutdown):

```json
{
  "equipment": [
    {"id": "PUMP-1", "downtime": [{"start": 60, "end": 90}]}
  ],
  "crew_size": 4,
  "horizon": 480,
  "budget_ms": 500,
  "operations": [
    {
      "id": "op-1",
      "equipment": "PUMP-1",
      "duration": 30,
      "workers": 2,
      "earliest_start": 10,
      "dependencies": []
    }
  ]
}
```

Limits and rules:

- At most 12 operations; `horizon` is between 1 and 480 minutes.
- All intervals are half-open `[start, end)`: back-to-back placement is legal.
- Every operation runs on one piece of equipment, is non-preemptible, starts
  only after all dependencies finish, must stay inside `[0, horizon)`, and
  cannot overlap the equipment's downtime windows.
- Parallel work is allowed across equipment, but the sum of `workers` at any
  minute cannot exceed `crew_size`.
- Validation errors return HTTP 400 with a `fields` array; each entry pinpoints
  the offending field (`operations[2].dependencies[0]`, `horizon`, ...) and
  covers illegal values, duplicate IDs, unknown equipment/operation references
  and dependency cycles.

On success the response lists each scheduled operation with `start`/`end`, the
total `makespan`, and solver statistics:

```json
{
  "status": "optimal",
  "schedule": [
    {"id": "op-1", "equipment": "PUMP-1", "start": 20, "end": 50, "workers": 2}
  ],
  "makespan": 50,
  "elapsed_ms": 1,
  "nodes_explored": 17
}
```

### Status meanings

| Status | Meaning |
| --- | --- |
| `optimal` | Search exhausted; the returned complete plan is proven to have minimum makespan. |
| `infeasible` | Search exhausted; no complete plan exists within the constraints. No schedule is returned. |
| `timeout_feasible` | `budget_ms` was reached; a complete feasible plan is returned but optimality was not proven. |
| `timeout_unknown` | `budget_ms` was reached before any complete plan was found. This is **not** "infeasible"; no schedule is returned. |
| `canceled` | The request was canceled (client disconnect/context) and the search stopped immediately. |

`budget_ms` <= 0 means unlimited. A budget never carries over between requests:
every request gets an isolated solver, and cancellation only affects its own
search.

## Search strategy

The solver is an exact depth-first branch-and-bound search, not a greedy
dispatcher: it deliberately considers idle/wait options whenever they can
improve the result.

- **Candidate times**: a closure of all possible start times is precomputed
  from earliest starts, downtime ends and operation durations. In any optimal
  schedule an operation starts at one of these events, so enumerating them
  covers solutions that actively wait for other work or resources.
- **Branching**: among operations whose dependencies are placed, the one with
  the fewest currently feasible start times is chosen first (fail-first);
  start times are tried ascending.
- **Bounding**: nodes whose resource-free critical path (longest dependency
  chain to completion) cannot beat the incumbent makespan are pruned.
- **Tie break**: among minimum-makespan plans the lexicographically smallest
  start-time vector over operation IDs sorted ascending is returned.
- **Stopping**: the deadline and request context are checked while searching;
  optimality/infeasibility is claimed only after the search space is exhausted.
