package launch

import "strings"

// launchProgram is the command a session is actually spawned with: the base
// program continued natively when p.ResumeArgs is set, seeded with p.InitialPrompt
// when that is set, or the bare program otherwise. The session's stored Program
// stays the bare program regardless, so re-resume and display are unaffected.
func launchProgram(program string, p Params) string {
	switch {
	case len(p.ResumeArgs) > 0:
		return resumeProgram(program, p.ResumeArgs)
	case p.InitialPrompt != "":
		return seedProgram(program, p.InitialPrompt)
	default:
		return program
	}
}

// resumeProgram returns the launch command that continues an agent session
// natively: the base program with its agent's resume argv appended, e.g.
// "claude --resume <id>". The args carry a session id with no shell
// metacharacters, so a plain join is safe.
func resumeProgram(program string, args []string) string {
	return program + " " + strings.Join(args, " ")
}

// seedProgram returns the launch command that starts program with prompt as its
// first message, so a resumed session whose agent has no native resume still
// continues from the recorded context. The prompt is passed as one positional
// argument, shell-quoted, and the program string is run through the shell by
// tmux.
//
// ponytail: a positional prompt fits claude/codex/gemini/copilot/cursor; an
// agent that needs a prompt flag would get a per-agent override added here.
func seedProgram(program, prompt string) string {
	return program + " " + shellQuote(prompt)
}

// shellQuote wraps s in single quotes for /bin/sh, escaping embedded single
// quotes, so a multi-line prompt survives as a single argument.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
