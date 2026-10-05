package hooks

import (
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/flexdinesh/servediff/internal/processlock"
)

const maxActivityBytes = 4096

// Activity contains collection and delivery metadata, never captured contents.
type Activity struct {
	Time          time.Time `json:"time"`
	Stage         string    `json:"stage,omitempty"`
	Status        string    `json:"status,omitempty"`
	Job           string    `json:"job,omitempty"`
	InputPath     string    `json:"inputPath,omitempty"`
	Path          string    `json:"path,omitempty"`
	Agent         string    `json:"agent,omitempty"`
	RunID         string    `json:"runId,omitempty"`
	Branch        string    `json:"branch,omitempty"`
	BranchID      string    `json:"branchId,omitempty"`
	RepositoryKey string    `json:"repositoryKey,omitempty"`
	CheckoutKey   string    `json:"checkoutKey,omitempty"`
	Base          string    `json:"base,omitempty"`
	Head          string    `json:"head,omitempty"`
	Destination   string    `json:"destination,omitempty"`
	SubmissionID  string    `json:"submissionId,omitempty"`
	ContextID     string    `json:"contextId,omitempty"`
	Attempt       int       `json:"attempt,omitempty"`
	FileCount     int       `json:"fileCount,omitempty"`
	Reason        string    `json:"reason,omitempty"`
	Error         string    `json:"error,omitempty"`
}

// Record persists a bounded JSONL activity entry. Logging failures never affect
// collection. The short blocking lock preserves concurrent lifecycle events.
func (engine Engine) Record(activity Activity) {
	engine.recordActivity("", activity)
}

// RecordJob also atomically replaces the job's readable latest status. Callers
// supply stage/status and can override event metadata (e.g. its original path).
func (engine Engine) RecordJob(job string, event Event, activity Activity) {
	activity.Job = job
	if activity.InputPath == "" {
		activity.InputPath = event.InputPath
		if activity.InputPath == "" {
			activity.InputPath = event.Path
		}
	}
	if activity.Path == "" {
		activity.Path = event.Path
	}
	if activity.Agent == "" {
		activity.Agent = event.Agent
	}
	if activity.RunID == "" {
		activity.RunID = event.RunID
	}
	directory, _ := engine.jobPath(job)
	engine.recordActivity(directory, activity)
}

func (engine Engine) recordActivity(directory string, activity Activity) {
	activity = boundedActivity(activity)
	data, err := json.Marshal(activity)
	if err != nil {
		return
	}
	lock, err := processlock.Acquire(filepath.Join(engine.Directory, "log.lock"))
	if err != nil {
		return
	}
	defer lock.Close()
	if directory != "" {
		_ = writeActivityStatus(filepath.Join(directory, "status.json"), activity)
	}
	path := filepath.Join(engine.Directory, "hooks.log")
	if info, err := os.Stat(path); err == nil && info.Size()+int64(len(data)+1) > maxLogBytes {
		if os.Chmod(path, 0o600) != nil {
			return
		}
		backup := path + ".1"
		if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
			return
		}
		if os.Rename(path, backup) != nil {
			return
		}
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	if file.Chmod(0o600) != nil {
		return
	}
	_, _ = file.Write(append(data, '\n'))
}

func writeActivityStatus(path string, activity Activity) error {
	data, err := json.MarshalIndent(activity, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".status-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.Write(append(data, '\n')); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), path)
}

var activityURL = regexp.MustCompile(`https?://[^\s"'<>]+`)

func boundedActivity(activity Activity) Activity {
	if activity.Time.IsZero() {
		activity.Time = time.Now().UTC()
	}
	fields := []*string{
		&activity.Stage, &activity.Status, &activity.Job, &activity.InputPath,
		&activity.Path, &activity.Agent, &activity.RunID, &activity.Branch,
		&activity.BranchID, &activity.RepositoryKey, &activity.CheckoutKey,
		&activity.Base, &activity.Head, &activity.Destination, &activity.SubmissionID,
		&activity.ContextID, &activity.Reason, &activity.Error,
	}
	for _, field := range fields {
		*field = redactActivity(*field)
	}
	for {
		data, _ := json.Marshal(activity)
		if len(data) <= maxActivityBytes {
			return activity
		}
		longest := fields[0]
		for _, field := range fields[1:] {
			if len(*field) > len(*longest) {
				longest = field
			}
		}
		limit := len(*longest) / 2
		for limit > 0 && !utf8.ValidString((*longest)[:limit]) {
			limit--
		}
		*longest = (*longest)[:limit]
	}
}

func redactActivity(value string) string {
	if token := os.Getenv("SERVEDIFF_TOKEN"); token != "" {
		value = strings.ReplaceAll(value, token, "[redacted]")
	}
	return activityURL.ReplaceAllStringFunc(value, func(value string) string {
		address, err := url.Parse(value)
		if err != nil {
			return "[redacted-url]"
		}
		address.User = nil
		address.RawQuery = ""
		address.ForceQuery = false
		address.Fragment = ""
		address.RawFragment = ""
		return address.String()
	})
}
