package launch

import "testing"

func TestLaunchProgramSeedsPositionally(t *testing.T) {
	got := launchProgram("claude", Params{InitialPrompt: "fix the bug"})
	if want := `claude 'fix the bug'`; got != want {
		t.Fatalf("launchProgram = %q, want %q", got, want)
	}
}

// Copilot CLI takes no positional arguments: a bare prompt makes it exit with
// "too many arguments" before the session starts, so its prompt must arrive
// behind -p.
func TestLaunchProgramSeedsBehindAPromptFlag(t *testing.T) {
	got := launchProgram("copilot", Params{InitialPrompt: "fix the bug"})
	if want := `copilot -p 'fix the bug'`; got != want {
		t.Fatalf("launchProgram = %q, want %q", got, want)
	}
}

func TestLaunchProgramSeedsBehindAPromptFlagWithExistingArgs(t *testing.T) {
	got := launchProgram(
		"copilot --allow-all-tools",
		Params{InitialPrompt: "fix the bug"},
	)
	if want := `copilot --allow-all-tools -p 'fix the bug'`; got != want {
		t.Fatalf("launchProgram = %q, want %q", got, want)
	}
}

func TestLaunchProgramQuotesAwkwardPrompts(t *testing.T) {
	got := launchProgram("copilot", Params{
		InitialPrompt: "don't stop\nkeep going",
	})
	if want := `copilot -p 'don'\''t stop` + "\n" + `keep going'`; got != want {
		t.Fatalf("launchProgram = %q, want %q", got, want)
	}
}

func TestLaunchProgramWithoutAPrompt(t *testing.T) {
	if got := launchProgram("copilot", Params{}); got != "copilot" {
		t.Fatalf("launchProgram = %q, want %q", got, "copilot")
	}
}

func TestLaunchProgramPrefersResume(t *testing.T) {
	got := launchProgram("copilot", Params{
		ResumeArgs:    []string{"--resume", "abc"},
		InitialPrompt: "ignored",
	})
	if want := "copilot --resume abc"; got != want {
		t.Fatalf("launchProgram = %q, want %q", got, want)
	}
}
