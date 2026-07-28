//go:build !windows

package gitremote

import (
	"os/exec"
	"strings"
	"testing"
)

func TestConfigureRecordsTheRemoteAndItsCore(t *testing.T) {
	dir := gitRepo(t)

	if err := Configure(
		dir, RemoteName, "acme/widgets", "https://core.example",
	); err != nil {
		t.Fatalf("Configure: %v", err)
	}
	if got := config(t, dir, "remote."+RemoteName+".url"); got !=
		"wasa://acme/widgets" {
		t.Errorf("remote url = %q, want wasa://acme/widgets", got)
	}
	if got := ConfiguredCore(dir, RemoteName); got != "https://core.example" {
		t.Errorf("ConfiguredCore = %q, want https://core.example", got)
	}
}

// TestConfigureRepointsAnExistingRemote is what makes wasa link idempotent: a
// repo recreated on the core is re-pointed rather than reported as a conflict.
func TestConfigureRepointsAnExistingRemote(t *testing.T) {
	dir := gitRepo(t)
	mustConfigure(t, dir, "acme/widgets", "https://core.example")

	if err := Configure(
		dir, RemoteName, "acme/gadgets", "https://other.example",
	); err != nil {
		t.Fatalf("Configure again: %v", err)
	}
	if got := config(t, dir, "remote."+RemoteName+".url"); got !=
		"wasa://acme/gadgets" {
		t.Errorf("remote url = %q, want wasa://acme/gadgets", got)
	}
	if got := ConfiguredCore(dir, RemoteName); got != "https://other.example" {
		t.Errorf("ConfiguredCore = %q, want https://other.example", got)
	}
}

func TestUnconfigureRemovesTheRemoteAndItsCore(t *testing.T) {
	dir := gitRepo(t)
	mustConfigure(t, dir, "acme/widgets", "https://core.example")

	if !Unconfigure(dir, RemoteName) {
		t.Fatal("Unconfigure reported nothing to remove")
	}
	if got := ConfiguredCore(dir, RemoteName); got != "" {
		t.Errorf("ConfiguredCore after unlink = %q, want empty", got)
	}
	if Unconfigure(dir, RemoteName) {
		t.Error("Unconfigure removed a remote twice")
	}
}

func TestConfiguredCoreIsEmptyWithoutAPin(t *testing.T) {
	dir := gitRepo(t)
	if got := ConfiguredCore(dir, RemoteName); got != "" {
		t.Errorf("ConfiguredCore = %q, want empty", got)
	}
	if got := ConfiguredCore(dir, ""); got != "" {
		t.Errorf("ConfiguredCore for an unnamed remote = %q, want empty", got)
	}
	if got := ConfiguredCore(t.TempDir(), RemoteName); got != "" {
		t.Errorf("ConfiguredCore outside a repo = %q, want empty", got)
	}
}

func gitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	out, err := exec.Command("git", "-C", dir, "init").CombinedOutput()
	if err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return dir
}

func mustConfigure(t *testing.T, dir, slug, coreURL string) {
	t.Helper()
	if err := Configure(dir, RemoteName, slug, coreURL); err != nil {
		t.Fatalf("Configure: %v", err)
	}
}

func config(t *testing.T, dir, key string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "config", "--get", key).Output()
	if err != nil {
		t.Fatalf("git config --get %s: %v", key, err)
	}
	return strings.TrimSpace(string(out))
}
