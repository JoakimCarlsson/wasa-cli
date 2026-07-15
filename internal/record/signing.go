package record

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// SignPolicy is the resolved record.sign config a checkpoint write honors:
// Enabled turns on signing at all, using whatever signing setup (openpgp or
// ssh) the repository's git config already carries for the user's own
// commits; Require makes an unavailable key skip the write — with a warning
// — instead of falling back to an unsigned commit. The zero value disables
// signing, so callers that never resolve a policy (tests, import) get
// today's unsigned behavior unchanged.
type SignPolicy struct {
	Enabled bool
	Require bool
}

// signPolicyFor resolves the sign policy from the cockpit config stored
// under home ($WASA_HOME). A missing or unreadable config degrades to the
// zero policy (signing off) rather than failing the caller: recording is
// best-effort by contract and config trouble must never block a checkpoint.
func signPolicyFor(home string) SignPolicy {
	cfg, err := config.Load(home)
	if err != nil {
		return SignPolicy{}
	}
	return SignPolicy{Enabled: cfg.Sign.Enabled, Require: cfg.Sign.Require}
}

// signingKeyConfigured reports whether repoDir's git config names a signing
// key (user.signingkey), the one setting both the openpgp and the ssh
// gpg.format need to sign a commit. It is the "no key available" check the
// sign policy's default (non-require) fallback and require-mode skip both
// key off.
func signingKeyConfigured(repoDir string) bool {
	key, err := gitIn(repoDir, nil, "config", "--get", "user.signingkey")
	return err == nil && strings.TrimSpace(key) != ""
}

// warnSignOnce keys a one-time-per-process warning so a long-lived process
// (the cockpit, a burst of commit-linked checkpoints) does not spam the same
// signing warning on every write.
var warnSignOnce sync.Map

func warnSignOnceLog(key, format string, args ...any) {
	if _, already := warnSignOnce.LoadOrStore(key, true); already {
		return
	}
	log.Printf(format, args...)
}

// commitTree creates the commit object for treeSHA, signing it when sign is
// enabled and a signing key is configured. A configured key that still fails
// to sign (revoked, agent unreachable, wrong passphrase) falls back to an
// unsigned commit exactly like a missing key does, unless sign.Require is
// set, in which case the write is skipped: signing must never crash or block
// a session, but "require" means exactly what it says.
func commitTree(
	repoDir, treeSHA, subject string, dateEnv []string, sign SignPolicy,
) (string, error) {
	if !sign.Enabled {
		return gitInEnv(
			repoDir, nil, dateEnv, "commit-tree", treeSHA, "-m", subject,
		)
	}
	if !signingKeyConfigured(repoDir) {
		if sign.Require {
			return "", fmt.Errorf(
				"signing required but no signing key is configured " +
					"(git config user.signingkey)",
			)
		}
		warnSignOnceLog(
			"no-key",
			"wasa: record signing enabled but no signing key is "+
				"configured (git config user.signingkey); writing unsigned",
		)
		return gitInEnv(
			repoDir, nil, dateEnv, "commit-tree", treeSHA, "-m", subject,
		)
	}
	commit, err := gitInEnv(
		repoDir, nil, dateEnv, "commit-tree", treeSHA, "-S", "-m", subject,
	)
	if err == nil {
		return commit, nil
	}
	if sign.Require {
		return "", fmt.Errorf("signed checkpoint commit failed: %w", err)
	}
	warnSignOnceLog(
		"sign-failed",
		"wasa: record signing failed (%v); writing unsigned", err,
	)
	return gitInEnv(
		repoDir, nil, dateEnv, "commit-tree", treeSHA, "-m", subject,
	)
}

// SignatureStatus is how a checkpoint commit's signature verified: see
// VerifyCommit.
type SignatureStatus string

// Signature statuses. Unsigned and Unknown both mean "no assertion can be
// made", but are kept distinct because they have different causes: Unsigned
// is a record nobody tried to sign, Unknown is a signature that could not be
// checked (missing key, expired, or a git too old to understand it).
const (
	SignatureUnsigned SignatureStatus = "unsigned"
	SignatureGood     SignatureStatus = "good"
	SignatureBad      SignatureStatus = "bad"
	SignatureUnknown  SignatureStatus = "unknown"
)

// VerifyCommit reports sha's signature status the same way `git
// verify-commit` would, without ever reaching the network for a missing key:
// git's own %G? commit log format, which understands both openpgp and ssh
// signatures. A missing keyring or an unknown signer reports Unknown, never
// an error — verification is read-only and best-effort, same as the rest of
// the read path.
func VerifyCommit(repoDir, sha string) SignatureStatus {
	out, err := gitIn(repoDir, nil, "log", "-1", "--format=%G?", sha)
	if err != nil {
		return SignatureUnknown
	}
	switch strings.TrimSpace(out) {
	case "N", "":
		return SignatureUnsigned
	case "G", "U":
		return SignatureGood
	case "B", "R":
		return SignatureBad
	default: // E (cannot check, e.g. missing key), X/Y (expired sig/key)
		return SignatureUnknown
	}
}
