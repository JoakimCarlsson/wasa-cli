package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("1.2.3", []string{"--version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "wasa version 1.2.3" {
		t.Fatalf("stdout = %q, want %q", got, "wasa version 1.2.3")
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunVersionJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("1.2.3", []string{"version", "--json"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}

	var got versionJSON
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf(
			"stdout is not valid JSON: %v (stdout=%q)",
			err,
			stdout.String(),
		)
	}
	if got.Version != "1.2.3" {
		t.Fatalf("version = %q, want %q", got.Version, "1.2.3")
	}
	if got.Contract < 1 {
		t.Fatalf("contract = %d, want >= 1", got.Contract)
	}
}

func TestRunVersionSubcommandHuman(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("1.2.3", []string{"version"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if got := strings.TrimSpace(stdout.String()); got != "wasa version 1.2.3" {
		t.Fatalf("stdout = %q, want %q", got, "wasa version 1.2.3")
	}
}

func TestRunVersionBadFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("1.2.3", []string{"version", "--nope"}, &stdout, &stderr)

	if code == 0 {
		t.Fatalf("exit code = 0, want non-zero for a bad flag")
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty (no success document on error)",
			stdout.String())
	}
}

func TestRunHelp(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		var stdout, stderr bytes.Buffer
		code := run("dev", []string{arg}, &stdout, &stderr)

		if code != 0 {
			t.Fatalf("%s: exit code = %d, want 0", arg, code)
		}
		out := stdout.String()
		if !strings.Contains(out, "Usage:") {
			t.Fatalf("%s: stdout missing usage text: %q", arg, out)
		}
		if !strings.Contains(out, "wasa version dev") {
			t.Fatalf("%s: stdout missing version line: %q", arg, out)
		}
	}
}

func TestRunNoArgs(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("dev", nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "Usage:") {
		t.Fatalf("stdout missing usage text: %q", stdout.String())
	}
}

func TestRunUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("dev", []string{"bogus"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("stderr missing unknown-command message: %q", stderr.String())
	}
}

func TestRunUnknownFlag(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run("dev", []string{"--nope"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("exit code = %d, want 2", code)
	}
}
