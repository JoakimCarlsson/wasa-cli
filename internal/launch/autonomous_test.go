package launch

import "testing"

func TestBaseExe(t *testing.T) {
	cases := map[string]string{
		"":                                      "",
		"claude":                                "claude",
		"  claude  ":                            "claude",
		"/usr/bin/claude --resume":              "claude",
		"claude --dangerously-skip-permissions": "claude",
		"/bin/bash":                             "bash",
	}
	for in, want := range cases {
		if got := baseExe(in); got != want {
			t.Errorf("baseExe(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAutonomousFlagKnownAndUnknown(t *testing.T) {
	if flag, ok := AutonomousFlag("claude"); !ok ||
		flag != "--dangerously-skip-permissions" {
		t.Errorf(
			"AutonomousFlag(claude) = %q,%v; want the skip-permissions flag",
			flag, ok,
		)
	}
	if flag, ok := AutonomousFlag("/usr/bin/codex --model x"); !ok ||
		flag != "--dangerously-bypass-approvals-and-sandbox" {
		t.Errorf("AutonomousFlag(codex) = %q,%v; want codex flag", flag, ok)
	}
	if _, ok := AutonomousFlag("bash"); ok {
		t.Error("AutonomousFlag(bash) reported a flag; want none")
	}
	if AutonomousAvailable("/bin/zsh") {
		t.Error("AutonomousAvailable(zsh) = true; want false")
	}
}

func TestWithAutonomousAppends(t *testing.T) {
	cases := map[string]string{
		"claude":  "claude --dangerously-skip-permissions",
		"gemini":  "gemini --yolo",
		"copilot": "copilot --allow-all-tools",
	}
	for in, want := range cases {
		if got := WithAutonomous(in); got != want {
			t.Errorf("WithAutonomous(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestWithAutonomousDeduplicates(t *testing.T) {
	// The canonical flag already present must not be appended a second time.
	in := "claude --dangerously-skip-permissions"
	if got := WithAutonomous(in); got != in {
		t.Errorf("WithAutonomous(%q) = %q, want unchanged", in, got)
	}
	// An alias that already implies autonomy suppresses the canonical append.
	alias := "gemini --approval-mode yolo"
	if got := WithAutonomous(alias); got != alias {
		t.Errorf("WithAutonomous(%q) = %q, want unchanged (alias)", alias, got)
	}
}

func TestWithAutonomousNoFlagForUnknown(t *testing.T) {
	for _, in := range []string{"bash", "/bin/zsh -l", ""} {
		if got := WithAutonomous(in); got != in {
			t.Errorf("WithAutonomous(%q) = %q, want unchanged", in, got)
		}
	}
}
