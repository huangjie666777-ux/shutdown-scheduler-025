package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func post(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/schedule", strings.NewReader(body))
	rec := httptest.NewRecorder()
	NewRouter().ServeHTTP(rec, req)
	return rec
}

func TestScheduleEndpointOptimal(t *testing.T) {
	body := `{
		"equipment": [{"id": "E1", "downtime": [{"start": 10, "end": 20}]}, {"id": "E2"}],
		"crew_size": 2,
		"horizon": 120,
		"budget_ms": 1000,
		"operations": [
			{"id": "A", "equipment": "E1", "duration": 10, "workers": 1, "earliest_start": 0},
			{"id": "B", "equipment": "E1", "duration": 5, "workers": 1, "earliest_start": 0, "dependencies": ["A"]},
			{"id": "C", "equipment": "E2", "duration": 8, "workers": 2, "earliest_start": 0}
		]
	}`
	rec := post(t, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
	var res struct {
		Status   string `json:"status"`
		Makespan int    `json:"makespan"`
		Schedule []struct {
			ID    string `json:"id"`
			Start int    `json:"start"`
			End   int    `json:"end"`
		} `json:"schedule"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != "optimal" || len(res.Schedule) != 3 {
		t.Fatalf("unexpected result: %+v", res)
	}
	// A must end by 10 (before downtime), B starts at A's end.
	var a, b struct{ start, end int }
	for _, s := range res.Schedule {
		switch s.ID {
		case "A":
			a.start, a.end = s.Start, s.End
		case "B":
			b.start, b.end = s.Start, s.End
		}
	}
	if a.end > 10 || b.start < a.end {
		t.Fatalf("constraint violated: A=%v B=%v", a, b)
	}
}

func TestScheduleEndpointValidationError(t *testing.T) {
	body := `{"equipment": [], "crew_size": 0, "horizon": 999, "budget_ms": 10,
		"operations": [{"id": "A", "equipment": "NOPE", "duration": 1, "workers": 1, "earliest_start": 0, "dependencies": ["B"]}]}`
	rec := post(t, body)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
	var res struct {
		Fields []struct {
			Field string `json:"field"`
		} `json:"fields"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Fields) == 0 {
		t.Fatalf("expected field errors, got %s", rec.Body.String())
	}
}

func TestScheduleEndpointBadJSON(t *testing.T) {
	rec := post(t, `{not json`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status=%d", rec.Code)
	}
}
