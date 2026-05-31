//go:build windows

package conpty

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeProbe(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.WriteFile(
		filepath.Join(dir, name),
		[]byte("rem probe\r\n"),
		0o755,
	); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

func TestResolveProgramWrapsCmd(t *testing.T) {
	dir := t.TempDir()
	writeProbe(t, dir, "foo.cmd")
	t.Setenv("PATH", dir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	got := resolveProgram([]string{"foo", "--flag"})
	if len(got) != 4 ||
		!strings.EqualFold(got[1], "/c") ||
		!strings.HasSuffix(strings.ToLower(got[2]), "foo.cmd") ||
		got[3] != "--flag" {
		t.Fatalf("resolveProgram cmd = %v", got)
	}
}

func TestResolveProgramWrapsPS1(t *testing.T) {
	dir := t.TempDir()
	writeProbe(t, dir, "bar.ps1")
	// .ps1 is deliberately absent from PATHEXT, forcing the shim fallback.
	t.Setenv("PATH", dir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	got := resolveProgram([]string{"bar"})
	if !strings.HasSuffix(strings.ToLower(got[len(got)-1]), "bar.ps1") {
		t.Fatalf("resolveProgram ps1 = %v (want trailing bar.ps1)", got)
	}
	if !slices.Contains(got, "-File") {
		t.Fatalf("resolveProgram ps1 = %v (want -File)", got)
	}
}

func TestResolveProgramExeDirect(t *testing.T) {
	dir := t.TempDir()
	writeProbe(t, dir, "baz.exe")
	t.Setenv("PATH", dir)
	t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")

	got := resolveProgram([]string{"baz"})
	if len(got) != 1 || !strings.HasSuffix(strings.ToLower(got[0]), "baz.exe") {
		t.Fatalf("resolveProgram exe = %v", got)
	}
}

func TestResolveProgramUnresolvedUnchanged(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	got := resolveProgram([]string{"definitely-not-installed"})
	if len(got) != 1 || got[0] != "definitely-not-installed" {
		t.Fatalf("resolveProgram unresolved = %v", got)
	}
}
