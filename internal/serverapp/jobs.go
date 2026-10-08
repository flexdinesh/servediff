package serverapp

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/flexdinesh/servediff/internal/contextservice"
	"github.com/flexdinesh/servediff/internal/ingestionqueue"
	"github.com/flexdinesh/servediff/internal/reviewdata"
)

func acceptJob(service *contextservice.Service, queue ingestionqueue.Queue, w http.ResponseWriter, r *http.Request) {
	input, ok := decodeIngestion(service, w, r)
	if !ok {
		return
	}
	job, err := queue.Accept(r.Context(), service.UserID(), input)
	if err != nil {
		jobError(w, err)
		return
	}
	w.Header().Set("Location", "/api/v2/ingestion-jobs/"+job.ID)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(job)
}

func getJob(service *contextservice.Service, queue ingestionqueue.Queue, w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		problem(w, 405, "Method not allowed")
		return
	}
	if expected := r.Header.Get("X-Servediff-State"); expected != "" && expected != service.UserID() {
		problem(w, 409, "Server database identity changed")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/v2/ingestion-jobs/")
	if id == "" || strings.Contains(id, "/") {
		problem(w, 404, "Job not found")
		return
	}
	job, err := queue.Get(r.Context(), service.UserID(), id)
	if err != nil {
		jobError(w, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(job)
}

func jobError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ingestionqueue.ErrFull):
		w.Header().Set("Retry-After", "2")
		problem(w, 429, err.Error())
	case errors.Is(err, reviewdata.ErrNotFound):
		problem(w, 404, "Job not found")
	case errors.Is(err, reviewdata.ErrSubmissionConflict):
		problem(w, 409, err.Error())
	default:
		applicationError(w, err)
	}
}
