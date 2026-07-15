package pane

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/backend"
	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// PreviewMsg carries a fresh pane capture delivered by a control-mode stream.
// gen identifies the stream that produced it so a message from a superseded or
// closed stream is ignored; ok is false when that stream's channel closed.
type PreviewMsg struct {
	gen     int
	content string
	ok      bool
}

// Preview owns the cockpit's live-stream state for the selected session: the
// latest pane capture, the live control-mode watcher, the tmux name the stream
// targets, and the generation tag that lets a delivery from a superseded stream
// be dropped. stream is the optional streaming capability and tmux the
// always-present session backend used for the fallback Capture poll; both are
// supplied at construction.
type Preview struct {
	stream backend.StreamingBackend
	tmux   backend.SessionBackend

	preview   string
	watcher   backend.Watcher
	watchName string
	watchGen  int
}

// NewPreview builds a Preview over the streaming capability (nil on a
// non-streaming backend) and the session backend it polls as a fallback.
func NewPreview(
	stream backend.StreamingBackend,
	tmux backend.SessionBackend,
) Preview {
	return Preview{stream: stream, tmux: tmux}
}

// SetTarget makes the live preview stream track name (the root passes the
// selected running session's tmux name, or "" to tear down). When the target
// changed it tears down the old stream and clears the stale preview; when
// streaming is available and no stream is live for the target it opens one,
// bumps the generation and returns the command that waits on it. It returns nil
// when nothing changed, when there is no target, or when streaming is
// unavailable or fails — in which cases the fallback tick polls Capture
// instead. Never blocks on a capture itself.
func (p *Preview) SetTarget(name string) tea.Cmd {
	if name != p.watchName {
		p.Close()
		p.watchName = name
		p.preview = ""
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

// Close tears down any live stream and invalidates in-flight PreviewMsgs by
// bumping the generation, so a late delivery from the closed stream is dropped
// rather than applied.
func (p *Preview) Close() {
	if p.watcher != nil {
		_ = p.watcher.Close()
		p.watcher = nil
	}
	p.watchGen++
}

// Apply handles a PreviewMsg from a stream. It ignores deliveries from a
// superseded stream, drops to the fallback poll when the stream closed, and
// otherwise stores the capture and re-arms the wait on the same stream.
func (p *Preview) Apply(msg PreviewMsg) tea.Cmd {
	if msg.gen != p.watchGen || p.watcher == nil {
		return nil
	}
	if !msg.ok {
		p.Close()
		return nil
	}
	p.preview = msg.content
	return waitPreview(msg.gen, p.watcher.Updates())
}

// PollOrReconnect runs on the fallback tick. With a live stream it does nothing
// (the stream delivers updates and runs its own safety captures). Otherwise it
// tries to (re)establish a stream for name, and failing that falls back to a
// one-shot Capture poll — the only path on a non-streaming backend (Windows)
// and the recovery path after a dropped connection.
func (p *Preview) PollOrReconnect(name string) tea.Cmd {
	if p.watcher != nil {
		return nil
	}
	if cmd := p.SetTarget(name); cmd != nil {
		return cmd
	}
	p.pollCapture(name)
	return nil
}

// pollCapture re-captures name with a one-shot Capture. An empty name — no
// running selection, or the Preview tab not active — clears the buffer and
// captures nothing, so the fallback poll, like the stream, does no work when
// another tab is shown. Errors are swallowed: the preview is a convenience, not
// a source of truth. This is the fallback when no stream is available; on the
// streaming path it does not run.
func (p *Preview) pollCapture(name string) {
	if name == "" {
		p.preview = ""
		return
	}
	if out, err := p.tmux.Capture(name); err == nil {
		p.preview = out
	}
}

// WatchedName is the tmux name the preview currently targets. It exists for
// status derivation only: the root's contentFor reuses the live capture for the
// focused session rather than re-capturing it.
func (p Preview) WatchedName() string {
	return p.watchName
}

// Capture returns the latest stored pane content and whether a live watcher is
// driving it. It exists for status derivation only: the root's contentFor
// reuses the live capture for the focused session rather than re-capturing it.
func (p Preview) Capture() (content string, live bool) {
	return p.preview, p.watcher != nil
}

// Body renders the Preview tab. running is whether the selected session is
// running; the root gates the no-session state and calls Body with running for
// a selected session. An exited session shows an explanatory state, and a
// running one shows the live capture or a waiting hint until output arrives.
func (p Preview) Body(t theme.Theme, running bool, w, h int) string {
	if !running {
		return t.DimStyle.Render("Session exited — nothing to preview.")
	}
	if strings.TrimSpace(ansi.Strip(p.preview)) == "" {
		return t.DimStyle.Render("Waiting for output…")
	}
	return renderCapture(p.preview, w, h)
}

// waitPreview blocks on the stream's update channel and reports the next
// capture as a PreviewMsg tagged with gen. Re-issued after each delivery to
// keep consuming the stream; never runs on the Update goroutine.
func waitPreview(gen int, ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		content, ok := <-ch
		return PreviewMsg{gen: gen, content: content, ok: ok}
	}
}
