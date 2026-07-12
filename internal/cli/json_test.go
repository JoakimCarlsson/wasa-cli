package cli

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/joakimcarlsson/wasa-cli/internal/record"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func TestOutputContractIsSet(t *testing.T) {
	if outputContract < 1 {
		t.Fatalf("outputContract = %d, want >= 1", outputContract)
	}
}

func TestEmitSessionsJSON(t *testing.T) {
	payload := sessionsJSON{Sessions: []*registry.Session{
		{
			ID:     "abc123",
			Title:  "demo",
			Branch: "feat/x",
			Status: registry.StatusRunning,
		},
	}}

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
	if len(got.Sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(got.Sessions))
	}
	if got.Sessions[0]["id"] != "abc123" {
		t.Fatalf("sessions[0].id = %v, want abc123", got.Sessions[0]["id"])
	}
	if got.Sessions[0]["status"] != registry.StatusRunning {
		t.Fatalf("sessions[0].status = %v, want %q",
			got.Sessions[0]["status"], registry.StatusRunning)
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
