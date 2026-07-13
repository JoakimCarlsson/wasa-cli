package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestOutputContractIsSet(t *testing.T) {
	if outputContract < 1 {
		t.Fatalf("outputContract = %d, want >= 1", outputContract)
	}
}

func TestEmitSessionsJSON(t *testing.T) {
	clean, fail := 0, 2
	sessions := []*registry.Session{
		{
			ID:     "abc123",
			Title:  "demo",
			Branch: "feat/x",
			Status: registry.StatusRunning,
		},
		{ID: "done", Status: registry.StatusExited, ExitCode: &clean},
		{ID: "boom", Status: registry.StatusExited, ExitCode: &fail},
		{ID: "killed", Status: registry.StatusExited},
		{ID: "held", Status: registry.StatusPaused},
	}
	payload := sessionsPayload(t.TempDir(), sessions, time.Now())

	var buf bytes.Buffer
	if err := emitJSON(&buf, payload); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}

	var got struct {
		Sessions []map[string]any `json:"sessions"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(got.Sessions) != len(sessions) {
		t.Fatalf("sessions len = %d, want %d", len(got.Sessions), len(sessions))
	}
	if got.Sessions[0]["id"] != "abc123" {
		t.Fatalf("sessions[0].id = %v, want abc123", got.Sessions[0]["id"])
	}
	if got.Sessions[0]["status"] != registry.StatusRunning {
		t.Fatalf("sessions[0].status = %v, want %q",
			got.Sessions[0]["status"], registry.StatusRunning)
	}
	wantActivity := []string{
		"running",
		"finished",
		"failed",
		"exited",
		"paused",
	}
	for i, want := range wantActivity {
		if got.Sessions[i]["activityStatus"] != want {
			t.Fatalf("sessions[%d].activityStatus = %v, want %q",
				i, got.Sessions[i]["activityStatus"], want)
		}
	}
	if _, ok := got.Sessions[0]["exitCode"]; ok {
		t.Fatal("running session serialized an exitCode")
	}
	if got.Sessions[1]["exitCode"] != float64(0) {
		t.Fatalf(
			"finished session exitCode = %v, want 0",
			got.Sessions[1]["exitCode"],
		)
	}
}

func TestEmitSessionCreatedJSON(t *testing.T) {
	s := &registry.Session{
		ID:       "sess1",
		Title:    "demo",
		Branch:   "feat/x",
		TmuxName: "wasa_ws_sess1",
		Status:   registry.StatusRunning,
	}
	payload := sessionCreatedPayload(t.TempDir(), s, time.Now())

	var buf bytes.Buffer
	if err := emitJSON(&buf, payload); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}

	var got struct {
		Session map[string]any `json:"session"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if got.Session == nil {
		t.Fatalf("payload has no session object: %q", buf.String())
	}
	if got.Session["id"] != "sess1" {
		t.Fatalf("session.id = %v, want sess1", got.Session["id"])
	}
	if got.Session["tmuxName"] != "wasa_ws_sess1" {
		t.Fatalf("session.tmuxName = %v, want wasa_ws_sess1",
			got.Session["tmuxName"])
	}
	if got.Session["activityStatus"] != "running" {
		t.Fatalf("session.activityStatus = %v, want running",
			got.Session["activityStatus"])
	}
}

func TestEmitWorkspacesJSON(t *testing.T) {
	payload := workspacesJSON{Workspaces: []*registry.Workspace{
		{ID: "ws01", Name: "afterdark", RepoPath: "/home/j/afterdark"},
	}}

	var buf bytes.Buffer
	if err := emitJSON(&buf, payload); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}

	var got struct {
		Workspaces []map[string]any `json:"workspaces"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(got.Workspaces) != 1 || got.Workspaces[0]["name"] != "afterdark" {
		t.Fatalf("workspaces = %+v, want one named afterdark", got.Workspaces)
	}
}

func TestEmitCheckpointsJSONFlattensMeta(t *testing.T) {
	payload := checkpointsJSON{Checkpoints: []checkpointJSON{{
		Meta:      record.Meta{SessionID: "s1", Branch: "feat/y"},
		CommitSHA: "deadbeef",
		State:     "finished",
	}}}

	var buf bytes.Buffer
	if err := emitJSON(&buf, payload); err != nil {
		t.Fatalf("emitJSON: %v", err)
	}

	var got struct {
		Checkpoints []map[string]any `json:"checkpoints"`
	}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("output is not valid JSON: %v (%q)", err, buf.String())
	}
	if len(got.Checkpoints) != 1 {
		t.Fatalf("checkpoints len = %d, want 1", len(got.Checkpoints))
	}
	cp := got.Checkpoints[0]
	if cp["sessionId"] != "s1" {
		t.Fatalf(
			"checkpoints[0].sessionId = %v, want s1 (meta must flatten to top level)",
			cp["sessionId"],
		)
	}
	if cp["state"] != "finished" {
		t.Fatalf("checkpoints[0].state = %v, want finished", cp["state"])
	}
}

func TestExplainPayload(t *testing.T) {
	matches := []record.Match{{
		Entry: record.Entry{
			CommitSHA: "deadbeef",
			Meta: record.Meta{
				SessionID: "s1", Branch: "feat/y", Imported: true,
			},
		},
		Intent:     "why it exists",
		Transcript: []byte(`{"role":"user","content":"hi"}` + "\n"),
	}}

	payload := explainPayload(matches, false)
	if len(payload.Checkpoints) != 1 {
		t.Fatalf("checkpoints len = %d, want 1", len(payload.Checkpoints))
	}
	cp := payload.Checkpoints[0]
	if cp.SessionID != "s1" || cp.CommitSHA != "deadbeef" {
		t.Fatalf("payload = %+v, want s1/deadbeef", cp)
	}
	if cp.State != "open, imported" {
		t.Fatalf("state = %q, want %q", cp.State, "open, imported")
	}
	if cp.Intent != "why it exists" {
		t.Fatalf("intent = %q", cp.Intent)
	}
	if !strings.Contains(cp.Transcript, "hi") {
		t.Fatalf("transcript = %q, want rendered content", cp.Transcript)
	}

	bare := explainPayload(matches, true)
	if bare.Checkpoints[0].Transcript != "" {
		t.Fatalf(
			"withoutTranscript kept transcript %q",
			bare.Checkpoints[0].Transcript,
		)
	}
}

func TestCheckpointState(t *testing.T) {
	open := record.Meta{}
	if got := checkpointState(open); got != "open" {
		t.Fatalf("open state = %q, want open", got)
	}

	imported := record.Meta{Imported: true}
	if got := checkpointState(imported); got != "open, imported" {
		t.Fatalf("imported state = %q, want %q", got, "open, imported")
	}
}
