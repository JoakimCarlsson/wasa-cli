package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa/internal/backend"
	"github.com/joakimcarlsson/wasa/internal/registry"
)

// previewMsg carries a fresh pane capture delivered by a control-mode stream.
// gen identifies the stream that produced it so a message from a superseded or
// closed stream is ignored; ok is false when that stream's channel closed.
type previewMsg struct {
	gen     int
	content string
	ok      bool
}

// previewPane is the live-preview feature machine: it owns the stream that
// tracks the selected session's tmux output. watchName is the session the
// preview targets (its tmux name, or "" for none); watcher is the live
// control-mode stream for it, or nil when streaming is unavailable, failed or
// dropped, in which case the fallback tick polls Capture; watchGen tags the
// active stream so a previewMsg from a superseded stream is ignored.
type previewPane struct {
	tmux   backend.SessionBackend
	stream backend.StreamingBackend

	content   string
	watcher   backend.Watcher
	watchName string
	watchGen  int
}

func newPreviewPane(tmux backend.SessionBackend, stream backend.StreamingBackend) previewPane {
	return previewPane{tmux: tmux, stream: stream}
}

// waitPreview blocks on the stream's update channel and reports the next
// capture as a previewMsg tagged with gen. Re-issued after each delivery to keep
// consuming the stream; never runs on the Update goroutine.
func waitPreview(gen int, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		content, ok := <-ch
		return previewMsg{gen: gen, content: content, ok: ok}
	}
}

// retarget makes the live preview stream track name (the previewTarget). When
// the target changed it tears down the old stream and clears the stale preview;
// when streaming is available and no stream is live for the target it opens one
// and returns the command that waits on it. It returns nil when nothing changed,
// when there is no running target, or when streaming is unavailable or fails —
// in which cases the fallback tick polls Capture instead. Never blocks on a
// capture itself.
func (p *previewPane) retarget(name string) tea.Cmd {
	if name != p.watchName {
		p.close()
		p.watchName = name
		p.content = ""
	}
	if name == "" || p.stream == nil || p.watcher != nil {
		return nil
	}
	w, err := p.stream.Watch(name)
	if err != nil {
		return nil
	}
	p.watcher = w
	p.watchGen++
	return waitPreview(p.watchGen, w.Updates())
}

// close tears down any live stream and invalidates in-flight previewMsgs by
// bumping the generation, so a late delivery from the closed stream is dropped
// rather than applied.
func (p *previewPane) close() {
	if p.watcher != nil {
		_ = p.watcher.Close()
		p.watcher = nil
	}
	p.watchGen++
}

// apply handles a previewMsg from a stream. It ignores deliveries from a
// superseded stream, drops to the fallback poll when the stream closed, and
// otherwise stores the capture and re-arms the wait on the same stream.
func (p *previewPane) apply(msg previewMsg) tea.Cmd {
	if msg.gen != p.watchGen || p.watcher == nil {
		return nil
	}
	if !msg.ok {
		p.close()
		return nil
	}
	p.content = msg.content
	return waitPreview(msg.gen, p.watcher.Updates())
}

// pollOrReconnect runs on the fallback tick. With a live stream it does nothing
// (the stream delivers updates and runs its own safety captures). Otherwise it
// tries to (re)establish a stream for name, and failing that falls back to a
// one-shot Capture poll — the only path on a non-streaming backend (Windows) and
// the recovery path after a dropped connection.
func (p *previewPane) pollOrReconnect(name string) tea.Cmd {
	if p.watcher != nil {
		return nil
	}
	if cmd := p.retarget(name); cmd != nil {
		return cmd
	}
	p.pollCapture(name)
	return nil
}

// pollCapture re-captures name with a one-shot Capture. An empty target — no
// running selection, or the Preview tab not active — clears the buffer and
// captures nothing, so the fallback poll, like the stream, does no work when
// another tab is shown. Errors are swallowed: the preview is a convenience, not
// a source of truth.
func (p *previewPane) pollCapture(name string) {
	if name == "" {
		p.content = ""
		return
	}
	if out, err := p.tmux.Capture(name); err == nil {
		p.content = out
	}
}

// liveContent returns the live capture for name when the preview is actively
// streaming it, so the status sweep can reuse the focused session's stream
// rather than re-capturing it.
func (p previewPane) liveContent(name string) (string, bool) {
	if name == p.watchName && p.watcher != nil {
		return p.content, true
	}
	return "", false
}

func (p previewPane) view(th Theme, s *registry.Session, w, h int) string {
	if s == nil {
		return th.dimStyle.Render("No session selected.")
	}
	if s.Status != registry.StatusRunning {
		return th.dimStyle.Render("Session exited — nothing to preview.")
	}
	if strings.TrimSpace(ansi.Strip(p.content)) == "" {
		return th.dimStyle.Render("Waiting for output…")
	}
	return renderCapture(p.content, w, h)
}
