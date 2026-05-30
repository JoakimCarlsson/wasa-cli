package worktree

import (
	"path/filepath"
	"strings"
)

// Params carries every input a placement strategy might need to compute a
// worktree path. A given Layout uses only the fields relevant to it; the
// superset keeps the strategy seam stable as new layouts are added.
type Params struct {
	// Home is the resolved $WASA_HOME root (e.g. ~/.wasa).
	Home string
	// Workspace identifies the workspace the worktree belongs to. For now it
	// may be derived locally (e.g. the repository name); the workspace
	// registry will later supply a content-addressed identifier.
	Workspace string
	// RepoPath is the absolute path of the source repository. It is unused by
	// the central layout but required by sibling and inside-repo layouts.
	RepoPath string
	// Branch is the branch the worktree checks out, unsanitized.
	Branch string
}

// Layout maps placement Params to an absolute worktree path. It is the single
// seam through which all path computation flows: selecting sibling or
// inside-repo placement later means passing a different Layout to a Manager,
// with no change at any call site.
type Layout func(Params) string

// Central is the default layout. It places worktrees under
// $WASA_HOME/worktrees/<workspace>/<branch>, with the branch sanitized into a
// single filesystem-safe path segment.
func Central(p Params) string {
	return filepath.Join(
		p.Home,
		"worktrees",
		p.Workspace,
		sanitizeBranch(p.Branch),
	)
}

// sanitizeBranch turns an arbitrary branch name into a single safe path
// segment: every character outside [A-Za-z0-9._-] becomes a dash, runs of
// dashes collapse to one, and leading/trailing dashes and dots are trimmed.
// For example "feature/x" becomes "feature-x".
func sanitizeBranch(branch string) string {
	var b strings.Builder
	b.Grow(len(branch))
	for _, r := range branch {
		if isSafeBranchRune(r) {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}

	collapsed := collapseDashes(b.String())
	trimmed := strings.Trim(collapsed, "-.")
	if trimmed == "" {
		return "branch"
	}
	return trimmed
}

func isSafeBranchRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z':
		return true
	case r >= 'A' && r <= 'Z':
		return true
	case r >= '0' && r <= '9':
		return true
	case r == '-' || r == '_' || r == '.':
		return true
	default:
		return false
	}
}

func collapseDashes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		if r == '-' {
			if !prevDash {
				b.WriteRune(r)
			}
			prevDash = true
			continue
		}
		prevDash = false
		b.WriteRune(r)
	}
	return b.String()
}
