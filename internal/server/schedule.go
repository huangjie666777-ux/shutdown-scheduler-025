package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/huangjie666777-ux/shutdown-scheduler-025/internal/scheduler"
)

const maxBodyBytes = 1 << 20

type errorResponse struct {
	Error  string                 `json:"error"`
	Fields []scheduler.FieldError `json:"fields,omitempty"`
}

func handleSchedule(w http.ResponseWriter, r *http.Request) {
	var req scheduler.Request
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var maxErr *http.MaxBytesError
		msg := "invalid JSON body"
		if errors.As(err, &maxErr) {
			msg = "request body too large"
		}
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: msg})
		return
	}
	if errs := scheduler.Validate(&req); len(errs) > 0 {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "validation failed", Fields: errs})
		return
	}
	result := scheduler.Solve(r.Context(), &req)
	status := http.StatusOK
	if result.Status == scheduler.StatusCanceled {
		status = 499 // client closed request
	}
	writeJSON(w, status, result)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
