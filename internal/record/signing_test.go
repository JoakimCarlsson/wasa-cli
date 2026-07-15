package record

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupSSHSigning generates a throwaway ssh signing key, wires dir's git
// config to sign with it (gpg.format=ssh, user.signingkey) and to verify
// against it (gpg.ssh.allowedSignersFile), matching the fixed
// wasa@localhost committer identity every checkpoint commit carries. Skips
// the test when ssh-keygen is unavailable rather than failing a machine that
// lacks it.
func setupSSHSigning(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_wasa_test")
	if out, err := exec.Command(
		"ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath, "-q",
	).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v: %s", err, out)
	}
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("read pub key: %v", err)
	}
	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(
		allowed, []byte("wasa@localhost "+string(pub)), 0o644,
	); err != nil {
		t.Fatalf("write allowed_signers: %v", err)
	}
	mustGit(t, dir, "config", "gpg.format", "ssh")
	mustGit(t, dir, "config", "user.signingkey", keyPath+".pub")
	mustGit(t, dir, "config", "gpg.ssh.allowedSignersFile", allowed)
}

func TestWriteSignsWhenEnabledAndKeyAvailable(t *testing.T) {
	dir := initRepo(t)
	setupSSHSigning(t, dir)

	ref := mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "signed-1", WasaVersion: "test"},
		Sign: SignPolicy{Enabled: true},
	})
	sha := mustGit(t, dir, "rev-parse", ref)

	if got := VerifyCommit(dir, sha); got != SignatureGood {
		t.Fatalf("VerifyCommit = %q, want %q", got, SignatureGood)
	}
	if out, err := exec.Command(
		"git", "-C", dir, "verify-commit", sha,
	).CombinedOutput(); err != nil {
		t.Fatalf("git verify-commit: %v: %s", err, out)
	}
}

func TestWriteUnsignedWhenSigningDisabled(t *testing.T) {
	dir := initRepo(t)
	setupSSHSigning(t, dir)

	ref := mustWrite(t, dir, Checkpoint{
		Meta: Meta{SessionID: "unsigned-1", WasaVersion: "test"},
	})
	sha := mustGit(t, dir, "rev-parse", ref)

	if got := VerifyCommit(dir, sha); got != SignatureUnsigned {
		t.Fatalf("VerifyCommit = %q, want %q", got, SignatureUnsigned)
	}
}

func TestWriteFallsBackToUnsignedWithNoKey(t *testing.T) {
	dir := initRepo(t)
	if signingKeyConfigured(dir) {
		t.Fatal("fresh repo unexpectedly has a signing key configured")
	}

	ref, err := Write(dir, Checkpoint{
		Meta: Meta{SessionID: "no-key-1", WasaVersion: "test"},
		Sign: SignPolicy{Enabled: true},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	sha := mustGit(t, dir, "rev-parse", ref)
	if got := VerifyCommit(dir, sha); got != SignatureUnsigned {
		t.Fatalf("VerifyCommit = %q, want %q", got, SignatureUnsigned)
	}
}

func TestWriteSkippedWhenRequiredWithNoKey(t *testing.T) {
	dir := initRepo(t)

	_, err := Write(dir, Checkpoint{
		Meta: Meta{SessionID: "require-no-key", WasaVersion: "test"},
		Sign: SignPolicy{Enabled: true, Require: true},
	})
	if err == nil {
		t.Fatal("Write: expected an error when signing is required with no key")
	}
}

func TestSigningKeyConfigured(t *testing.T) {
	dir := initRepo(t)
	if signingKeyConfigured(dir) {
		t.Fatal("fresh repo should report no signing key configured")
	}
	setupSSHSigning(t, dir)
	if !signingKeyConfigured(dir) {
		t.Fatal(
			"repo with user.signingkey should report a signing key configured",
		)
	}
}

func TestVerifyCommitUnknownForBogusSHA(t *testing.T) {
	dir := initRepo(t)
	if got := VerifyCommit(
		dir,
		"0000000000000000000000000000000000000000",
	); got != SignatureUnknown {
		t.Fatalf("VerifyCommit = %q, want %q", got, SignatureUnknown)
	}
}
