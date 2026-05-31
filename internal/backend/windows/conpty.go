//go:build windows

// Package conpty is wasa's native Windows session backend. It implements the
// backend.SessionBackend contract without tmux or WSL by driving the Windows
// pseudo-console API (ConPTY, Windows 10 1809+).
//
// Sessions must outlive the wasa process that spawns them, exactly as tmux
// sessions survive on the shared tmux server. ConPTY handles die with the
// process that owns them, so this package runs a background daemon that holds
// every session's pseudo-console and child process; the wasa CLI and TUI are
// thin clients that talk to the daemon over a per-user named pipe. The daemon
// auto-starts on first use and shuts itself down after a grace period with no
// sessions.
//
// This file is the lowest layer: a single pseudo-console plus the child process
// attached to it. Everything above it (sessions, scrollback, IPC, attach) is
// built on this primitive.
package conpty

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ErrUnsupported reports that the host Windows version predates the ConPTY API
// (Windows 10 1809 / build 17763). It mirrors tmux's "binary not found"
// handling: an actionable failure rather than a panic.
var ErrUnsupported = errors.New(
	"native Windows sessions require the pseudo-console API " +
		"(Windows 10 1809 or later); upgrade Windows or run wasa under WSL",
)

// supported reports whether the host exposes CreatePseudoConsole. It is the
// pre-flight guard that turns a pre-1809 host into ErrUnsupported instead of a
// syscall panic when the lazily-loaded procedure is missing.
func supported() bool {
	return windows.NewLazySystemDLL("kernel32").
		NewProc("CreatePseudoConsole").
		Find() == nil
}

// conPty is one pseudo-console and the child process running inside it. Callers
// write input through In, read output through Out, Resize on terminal changes,
// and Close to tear the whole thing down.
type conPty struct {
	hpc     windows.Handle
	process windows.Handle
	thread  windows.Handle

	ptyIn  windows.Handle
	ptyOut windows.Handle

	in  *pipeConn
	out *pipeConn

	closeOnce sync.Once
}

// startConPty creates a pseudo-console sized cols×rows, then launches argv inside
// it with working directory dir and environment env (KEY=VALUE entries merged
// over the daemon's own environment). It returns the live conPty or an error;
// ErrUnsupported is returned on pre-1809 hosts.
func startConPty(
	cols, rows int16,
	dir string,
	env, argv []string,
) (*conPty, error) {
	if !supported() {
		return nil, ErrUnsupported
	}
	if len(argv) == 0 {
		return nil, errors.New("conpty: empty program")
	}
	argv = resolveProgram(argv)

	var ptyIn, cmdIn, cmdOut, ptyOut windows.Handle
	if err := windows.CreatePipe(&ptyIn, &cmdIn, nil, 0); err != nil {
		return nil, fmt.Errorf("conpty: create input pipe: %w", err)
	}
	if err := windows.CreatePipe(&cmdOut, &ptyOut, nil, 0); err != nil {
		windows.CloseHandle(ptyIn)
		windows.CloseHandle(cmdIn)
		return nil, fmt.Errorf("conpty: create output pipe: %w", err)
	}

	var hpc windows.Handle
	err := windows.CreatePseudoConsole(
		windows.Coord{X: cols, Y: rows},
		ptyIn,
		ptyOut,
		0,
		&hpc,
	)
	if err != nil {
		windows.CloseHandle(ptyIn)
		windows.CloseHandle(ptyOut)
		windows.CloseHandle(cmdIn)
		windows.CloseHandle(cmdOut)
		return nil, fmt.Errorf("conpty: create pseudo console: %w", err)
	}

	pi, err := spawnAttached(hpc, dir, env, argv)
	if err != nil {
		windows.ClosePseudoConsole(hpc)
		windows.CloseHandle(ptyIn)
		windows.CloseHandle(ptyOut)
		windows.CloseHandle(cmdIn)
		windows.CloseHandle(cmdOut)
		return nil, err
	}

	return &conPty{
		hpc:     hpc,
		process: pi.Process,
		thread:  pi.Thread,
		ptyIn:   ptyIn,
		ptyOut:  ptyOut,
		in:      &pipeConn{h: cmdIn},
		out:     &pipeConn{h: cmdOut},
	}, nil
}

var (
	modkernel32 = windows.NewLazySystemDLL(
		"kernel32.dll",
	)
	procInitializeProcThreadAttributeList = modkernel32.NewProc(
		"InitializeProcThreadAttributeList",
	)
	procUpdateProcThreadAttribute = modkernel32.NewProc(
		"UpdateProcThreadAttribute",
	)
	procDeleteProcThreadAttributeList = modkernel32.NewProc(
		"DeleteProcThreadAttributeList",
	)
)

// startupInfoEx mirrors STARTUPINFOEXW: a STARTUPINFOW followed by a pointer to
// a process-thread attribute list. attributeList holds that list inline; its
// slice-header data word sits immediately after startupInfo, which is exactly
// where CreateProcess reads lpAttributeList from when Cb spans both fields.
type startupInfoEx struct {
	startupInfo   windows.StartupInfo
	attributeList []byte
}

// spawnAttached launches argv as a child of the current process bound to the
// pseudo-console hpc, returning its process information. The pseudo-console is
// passed through a PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE attribute rather than
// inherited std handles, so the child's stdin/stdout/stderr are the console.
func spawnAttached(
	hpc windows.Handle,
	dir string,
	env, argv []string,
) (*windows.ProcessInformation, error) {
	cmdLine, err := windows.UTF16PtrFromString(windows.ComposeCommandLine(argv))
	if err != nil {
		return nil, fmt.Errorf("conpty: command line: %w", err)
	}

	var dirPtr *uint16
	if dir != "" {
		if dirPtr, err = windows.UTF16PtrFromString(dir); err != nil {
			return nil, fmt.Errorf("conpty: working directory: %w", err)
		}
	}

	envBlock, err := mergedEnvBlock(env)
	if err != nil {
		return nil, err
	}

	flags := uint32(windows.EXTENDED_STARTUPINFO_PRESENT)
	if envBlock != nil {
		flags |= windows.CREATE_UNICODE_ENVIRONMENT
	}

	siEx, err := pseudoConsoleStartupInfo(hpc)
	if err != nil {
		return nil, err
	}
	defer procDeleteProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(&siEx.attributeList[0])),
	)

	pi := new(windows.ProcessInformation)
	if err := windows.CreateProcess(
		nil,
		cmdLine,
		nil,
		nil,
		false,
		flags,
		envBlock,
		dirPtr,
		&siEx.startupInfo,
		pi,
	); err != nil {
		return nil, fmt.Errorf("conpty: create process %q: %w", argv[0], err)
	}
	return pi, nil
}

// pseudoConsoleStartupInfo builds an extended startup info whose attribute list
// binds the child to pseudo-console hpc. For PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE
// the attribute value is the HPCON handle passed by value as lpValue, with size
// equal to the handle's size.
//
// STARTF_USESTDHANDLES is set (with the std-handle fields left zero) so the
// child does not inherit the parent's real console; without it the child binds
// to the inherited console instead of the pseudo-console and no output reaches
// the pipe.
func pseudoConsoleStartupInfo(hpc windows.Handle) (*startupInfoEx, error) {
	var siEx startupInfoEx
	siEx.startupInfo.Cb = uint32(
		unsafe.Sizeof(windows.StartupInfo{}) +
			unsafe.Sizeof(&siEx.attributeList[0]),
	)
	siEx.startupInfo.Flags |= windows.STARTF_USESTDHANDLES

	var size uintptr
	procInitializeProcThreadAttributeList.Call(
		0,
		1,
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	siEx.attributeList = make([]byte, size)

	r1, _, err := procInitializeProcThreadAttributeList.Call(
		uintptr(unsafe.Pointer(&siEx.attributeList[0])),
		1,
		0,
		uintptr(unsafe.Pointer(&size)),
	)
	if r1 == 0 {
		return nil, fmt.Errorf("conpty: initialize attribute list: %w", err)
	}

	r1, _, err = procUpdateProcThreadAttribute.Call(
		uintptr(unsafe.Pointer(&siEx.attributeList[0])),
		0,
		windows.PROC_THREAD_ATTRIBUTE_PSEUDOCONSOLE,
		uintptr(hpc),
		unsafe.Sizeof(hpc),
		0,
		0,
	)
	if r1 == 0 {
		return nil, fmt.Errorf("conpty: set pseudo console attribute: %w", err)
	}
	return &siEx, nil
}

// Resize matches the pseudo-console's buffer to a new terminal size.
func (c *conPty) Resize(cols, rows int16) error {
	return windows.ResizePseudoConsole(c.hpc, windows.Coord{X: cols, Y: rows})
}

// Exited reports whether the child process has terminated.
func (c *conPty) Exited() bool {
	s, err := windows.WaitForSingleObject(c.process, 0)
	return err == nil && s == windows.WAIT_OBJECT_0
}

// Close terminates the child, closes the pseudo-console, and releases every
// handle. It is safe to call more than once.
func (c *conPty) Close() {
	c.closeOnce.Do(func() {
		windows.ClosePseudoConsole(c.hpc)
		windows.TerminateProcess(c.process, 1)
		if c.in != nil {
			c.in.Close()
		}
		if c.out != nil {
			c.out.Close()
		}
		windows.CloseHandle(c.ptyIn)
		windows.CloseHandle(c.ptyOut)
		windows.CloseHandle(c.process)
		windows.CloseHandle(c.thread)
	})
}

// mergedEnvBlock returns a UTF-16, double-null-terminated environment block of
// the daemon's environment with each KEY=VALUE entry of extra applied on top, or
// nil when there is nothing to inject and the parent environment suffices.
func mergedEnvBlock(extra []string) (*uint16, error) {
	if len(extra) == 0 {
		return nil, nil
	}
	merged := mergeEnv(os.Environ(), extra)
	block, err := newEnvBlock(merged)
	if err != nil {
		return nil, fmt.Errorf("conpty: environment block: %w", err)
	}
	return block, nil
}
