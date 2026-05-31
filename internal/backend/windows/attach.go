//go:build windows

package conpty

import (
	"errors"
	"fmt"
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// prefixByte is the attach detach-chord prefix: Ctrl+B, mirroring tmux. Pressing
// Ctrl+B then 'd' detaches and returns to wasa with the session still running;
// Ctrl+B twice sends a literal Ctrl+B to the session.
const prefixByte = 0x02

// RunAttach is the body of the hidden "wasa __attach <name>" subcommand. It
// connects to the daemon, puts the real terminal into raw VT mode, and relays
// the terminal to the session's pseudo-console until the user detaches with the
// Ctrl+B d chord or the session ends. It is what AttachCmd's command runs, so
// tea.ExecProcess (the TUI) and the CLI attach path share one implementation.
func RunAttach(name string) error {
	if !supported() {
		return ErrUnsupported
	}
	if err := validateName(name); err != nil {
		return err
	}

	conn, err := dialPipe()
	if err != nil {
		return err
	}
	if conn == nil {
		return fmt.Errorf("session %q does not exist", name)
	}
	defer conn.Close()

	cols, rows := consoleSize()
	if err := writeFrame(conn, request{
		Op:   opAttach,
		Name: name,
		Cols: cols,
		Rows: rows,
	}); err != nil {
		return err
	}
	var resp response
	if err := readFrame(conn, &resp); err != nil {
		return err
	}
	if resp.Err != "" {
		return errors.New(resp.Err)
	}

	restore, err := enterRaw()
	if err != nil {
		return err
	}
	defer restore()

	done := make(chan error, 2)
	go func() {
		_, e := io.Copy(os.Stdout, conn)
		done <- e
	}()
	go func() {
		done <- relayInput(os.Stdin, conn)
	}()
	<-done
	return nil
}

// relayInput copies terminal input to the session, watching for the detach
// chord. It returns nil on detach and an error on a stream failure.
func relayInput(in io.Reader, conn io.Writer) error {
	buf := make([]byte, 512)
	armed := false
	for {
		n, err := in.Read(buf)
		if n > 0 {
			out, detach := filterChord(buf[:n], &armed)
			if len(out) > 0 {
				if _, werr := conn.Write(out); werr != nil {
					return werr
				}
			}
			if detach {
				return nil
			}
		}
		if err != nil {
			return err
		}
	}
}

// filterChord forwards input bytes, intercepting the Ctrl+B prefix. armed
// carries the "prefix seen" state across reads. It returns the bytes to forward
// and whether the detach chord (Ctrl+B then d) completed.
func filterChord(p []byte, armed *bool) (out []byte, detach bool) {
	for _, b := range p {
		if *armed {
			*armed = false
			switch b {
			case 'd', 'D':
				return out, true
			case prefixByte:
				out = append(out, prefixByte)
			default:
				out = append(out, prefixByte, b)
			}
			continue
		}
		if b == prefixByte {
			*armed = true
			continue
		}
		out = append(out, b)
	}
	return out, false
}

// enterRaw switches the console to raw VT mode for the attach and returns a
// function that restores the previous modes.
func enterRaw() (func(), error) {
	inH := windows.Handle(os.Stdin.Fd())
	outH := windows.Handle(os.Stdout.Fd())

	var inMode, outMode uint32
	if err := windows.GetConsoleMode(inH, &inMode); err != nil {
		return nil, fmt.Errorf("conpty: read stdin console mode: %w", err)
	}
	if err := windows.GetConsoleMode(outH, &outMode); err != nil {
		return nil, fmt.Errorf("conpty: read stdout console mode: %w", err)
	}

	rawIn := inMode &^ uint32(
		windows.ENABLE_LINE_INPUT|
			windows.ENABLE_ECHO_INPUT|
			windows.ENABLE_PROCESSED_INPUT,
	)
	rawIn |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	rawOut := outMode |
		windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING |
		windows.DISABLE_NEWLINE_AUTO_RETURN

	if err := windows.SetConsoleMode(inH, rawIn); err != nil {
		return nil, fmt.Errorf("conpty: set stdin console mode: %w", err)
	}
	if err := windows.SetConsoleMode(outH, rawOut); err != nil {
		windows.SetConsoleMode(inH, inMode)
		return nil, fmt.Errorf("conpty: set stdout console mode: %w", err)
	}

	return func() {
		windows.SetConsoleMode(inH, inMode)
		windows.SetConsoleMode(outH, outMode)
	}, nil
}

// consoleSize returns the terminal's column and row count, falling back to the
// daemon defaults when the size cannot be read.
func consoleSize() (int16, int16) {
	var info windows.ConsoleScreenBufferInfo
	err := windows.GetConsoleScreenBufferInfo(
		windows.Handle(os.Stdout.Fd()),
		&info,
	)
	if err != nil {
		return defaultCols, defaultRows
	}
	cols := info.Window.Right - info.Window.Left + 1
	rows := info.Window.Bottom - info.Window.Top + 1
	if cols <= 0 || rows <= 0 {
		return defaultCols, defaultRows
	}
	return cols, rows
}
