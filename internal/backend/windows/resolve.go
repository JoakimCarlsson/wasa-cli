//go:build windows

package conpty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// resolveProgram turns a session's program argv into a command line CreateProcess
// can actually launch. spawnAttached calls CreateProcess directly rather than
// through a shell, and CreateProcess runs only real executables: it ignores
// PATHEXT and cannot exec .cmd/.bat/.ps1 shims, which is how npm and editor
// installs ship CLIs such as copilot. resolveProgram resolves the program on
// PATH and, when it is a script shim, prefixes the matching interpreter so the
// pseudo-console launches it the way a shell would. A name that does not resolve
// is returned unchanged so CreateProcess surfaces its own error.
func resolveProgram(argv []string) []string {
	if len(argv) == 0 {
		return argv
	}
	path, ok := lookProgram(argv[0])
	if !ok {
		return argv
	}
	rest := argv[1:]
	switch strings.ToLower(filepath.Ext(path)) {
	case ".cmd", ".bat":
		return append([]string{comspec(), "/c", path}, rest...)
	case ".ps1":
		return append([]string{
			powershell(),
			"-NoLogo", "-NoProfile",
			"-ExecutionPolicy", "Bypass",
			"-File", path,
		}, rest...)
	default:
		return append([]string{path}, rest...)
	}
}

// lookProgram resolves name on PATH. It first tries exec.LookPath, which honors
// PATHEXT, then falls back to a .ps1 shim, which PATHEXT omits.
func lookProgram(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if dir == "" {
			continue
		}
		cand := filepath.Join(dir, name+".ps1")
		if info, err := os.Stat(cand); err == nil && !info.IsDir() {
			return cand, true
		}
	}
	return "", false
}

// comspec is the command interpreter used to run .cmd/.bat shims.
func comspec() string {
	if c := os.Getenv("ComSpec"); c != "" {
		return c
	}
	return "cmd.exe"
}

// powershell is the interpreter used to run .ps1 shims, preferring PowerShell 7
// (pwsh) and falling back to Windows PowerShell.
func powershell() string {
	if p, err := exec.LookPath("pwsh"); err == nil {
		return p
	}
	if p, err := exec.LookPath("powershell"); err == nil {
		return p
	}
	return "powershell.exe"
}
