package hooks

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func activities(t *testing.T, path string) []Activity {
	t.Helper()
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var entries []Activity
	for scanner.Scan() {
		if len(scanner.Bytes()) > maxActivityBytes {
			t.Fatalf("oversize activity: %d bytes", len(scanner.Bytes()))
		}
		var entry Activity
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("invalid activity JSON: %v", err)
		}
		if entry.Time.IsZero() {
			t.Fatal("activity lacks timestamp")
		}
		entries = append(entries, entry)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return entries
}

func TestActivityConcurrentEventsArePreserved(t *testing.T) {
	engine := Engine{Directory: t.TempDir()}
	var workers sync.WaitGroup
	for i := 0; i < 80; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			engine.Record(Activity{Stage: "ingestion", Status: "pending", SubmissionID: fmt.Sprint(i)})
		}()
	}
	workers.Wait()
	entries := activities(t, filepath.Join(engine.Directory, "hooks.log"))
	seen := map[string]bool{}
	for _, entry := range entries {
		seen[entry.SubmissionID] = true
	}
	if len(entries) != 80 || len(seen) != 80 {
		t.Fatalf("lost concurrent activities: %d entries, %d unique", len(entries), len(seen))
	}
}

func TestActivityEscapesPathsAndRedactsCredentials(t *testing.T) {
	t.Setenv("DIFFX_TOKEN", "super-secret-token")
	engine := Engine{Directory: t.TempDir()}
	path := "/tmp/new\ncheckout"
	engine.Record(Activity{
		Stage: "ingestion", Status: "waiting", Path: path,
		Destination: "http://username:password@localhost:7981/api?token=secret-query#secret-fragment",
		Error:       "super-secret-token: POST https://username:password@host.test/upload?access=hidden#secret failed",
	})
	logPath := filepath.Join(engine.Directory, "hooks.log")
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"super-secret-token", "username", "password", "secret-query", "secret-fragment", "access=hidden", "#secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("activity leaked %q", secret)
		}
	}
	entries := activities(t, logPath)
	if len(entries) != 1 || entries[0].Path != path || entries[0].Destination != "http://localhost:7981/api" {
		t.Fatalf("path or destination corrupted: %+v", entries)
	}
	if entries[0].Error != "[redacted]: POST https://host.test/upload failed" {
		t.Fatalf("unexpected redaction: %s", entries[0].Error)
	}
}

func TestActivityRotationKeepsPreviousLogAndValidJSON(t *testing.T) {
	engine := Engine{Directory: t.TempDir()}
	for i := 0; i < 100; i++ {
		engine.Record(Activity{Stage: "collection", Status: "error", Attempt: i + 1, Error: strings.Repeat("\"\n🌱", 6000)})
	}
	logPath := filepath.Join(engine.Directory, "hooks.log")
	for _, path := range []string{logPath, logPath + ".1"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > maxLogBytes || info.Mode().Perm() != 0o600 {
			t.Fatalf("log size/privacy: %s %v", path, info)
		}
		if len(activities(t, path)) == 0 {
			t.Fatalf("empty rotated log: %s", path)
		}
	}
	entries := activities(t, logPath)
	if entries[len(entries)-1].Attempt != 100 {
		t.Fatal("latest activity missing")
	}
	if _, err := os.Stat(logPath + ".2"); !os.IsNotExist(err) {
		t.Fatalf("rotation exceeded two files: %v", err)
	}
}

func TestActivityJobStatusRecordsLatestOutcomePrivately(t *testing.T) {
	engine := Engine{Directory: t.TempDir()}
	job := key("stable-checkout")
	event := Event{Path: "/tmp/moved-checkout", InputPath: "/tmp/workspace", Agent: "codex", RunID: "run-id"}
	engine.RecordJob(job, event, Activity{Stage: "ingestion", Status: "waiting", Attempt: 1, Error: "server unavailable"})
	engine.RecordJob(job, event, Activity{Stage: "collection", Status: "skipped", Reason: "unchanged", InputPath: "/tmp/original-input"})
	directory, err := engine.jobPath(job)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(directory, "status.json")
	var status Activity
	if err := readJSON(path, &status); err != nil {
		t.Fatal(err)
	}
	if status.Job != job || status.Path != event.Path || status.InputPath != "/tmp/original-input" || status.Agent != event.Agent || status.RunID != event.RunID || status.Status != "skipped" || status.Reason != "unchanged" || status.Error != "" || status.Attempt != 0 {
		t.Fatalf("unexpected latest status: %+v", status)
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("status privacy: %v %v", info, err)
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 {
		t.Fatalf("status temporary files leaked: %v %v", entries, err)
	}
	logged := activities(t, filepath.Join(engine.Directory, "hooks.log"))
	if len(logged) != 2 || logged[0].InputPath != event.InputPath {
		t.Fatalf("job provenance: %+v", logged)
	}
}

func TestActivityCorrectsExistingLogPermissions(t *testing.T) {
	engine := Engine{Directory: t.TempDir()}
	path := filepath.Join(engine.Directory, "hooks.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	engine.Log(errors.New("test error"))
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("existing log privacy: %v %v", info, err)
	}
}

func TestActivityFailureDoesNotAffectCollector(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	engine := Engine{Directory: path}
	engine.Record(Activity{Stage: "ingestion", Status: "error"})
	engine.RecordJob("invalid-job", Event{}, Activity{Status: "error"})
	engine.Log(nil)
}

func TestActivityBoundsAllMetadataAndPreservesTimestamp(t *testing.T) {
	engine := Engine{Directory: t.TempDir()}
	long := strings.Repeat("\x00🌱", 6000)
	at := time.Date(2026, time.October, 5, 12, 0, 0, 0, time.UTC)
	engine.Record(Activity{
		Time: at, Stage: long, Status: long, Job: long, InputPath: long,
		Path: long, Agent: long, RunID: long, Branch: long, BranchID: long,
		RepositoryKey: long, CheckoutKey: long, Base: long, Head: long,
		Destination: long, SubmissionID: long, ContextID: long, Reason: long, Error: long,
	})
	entries := activities(t, filepath.Join(engine.Directory, "hooks.log"))
	if len(entries) != 1 || !entries[0].Time.Equal(at) {
		t.Fatalf("bounded activity timestamp: %+v", entries)
	}
}
