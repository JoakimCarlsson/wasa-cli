package syntax

import (
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	"github.com/alecthomas/chroma/v2"
	"github.com/alecthomas/chroma/v2/lexers"
	"github.com/alecthomas/chroma/v2/styles"
)

// DefaultStyle is the Chroma style the cockpit colourises with when the config
// names none, or names one Chroma does not ship.
const DefaultStyle = "onedark"

// maxLineBytes is the longest line Highlighter tokenises. A minified bundle or
// an embedded blob costs more to lex than it gains in legibility, so a line
// past this length is returned in the base style untouched.
const maxLineBytes = 2048

// Highlighter colourises the lines of one file. It is built per file so the
// lexer lookup — the expensive part — happens once per diff header rather than
// once per line. The zero value is valid and returns every line unstyled beyond
// the base style, which is what an unrecognised file type gets.
type Highlighter struct {
	style *chroma.Style
	lexer chroma.Lexer
}

// styleCache memoises resolved Chroma styles by name. Styles are immutable once
// built and a diff resolves the same name for every file, so the lookup is done
// once per process rather than once per file.
var styleCache sync.Map

// New builds a Highlighter for path using the named Chroma style. An unknown
// style falls back to DefaultStyle and an unrecognised file type yields a
// Highlighter that leaves lines in the base style.
func New(styleName, path string) Highlighter {
	lexer := lexers.Match(path)
	if lexer == nil {
		return Highlighter{}
	}
	return Highlighter{
		style: resolveStyle(styleName),
		lexer: chroma.Coalesce(lexer),
	}
}

// Available reports whether h will colourise anything, so a caller can keep its
// cheaper unstyled path for a file type Chroma does not know.
func (h Highlighter) Available() bool { return h.lexer != nil }

// Line renders one source line, each token in base plus that token's colour and
// weight from the style. base carries the diff band's background, so the band
// runs unbroken under the colourised code. Tokenising is per line, so a state
// that spans lines — an open block comment, a multi-line string — restarts at
// each line; the cost of that is an occasional mis-tinted continuation, against
// a diff hunk that is in any case only a window onto the file.
func (h Highlighter) Line(base lipgloss.Style, line string) string {
	if h.lexer == nil || len(line) > maxLineBytes || line == "" {
		return base.Render(line)
	}
	it, err := h.lexer.Tokenise(nil, line)
	if err != nil {
		return base.Render(line)
	}
	var b strings.Builder
	for _, tok := range it.Tokens() {
		b.WriteString(h.token(base, tok))
	}
	return b.String()
}

// token renders one Chroma token in base, overlaid with the style's entry for
// that token type. A trailing newline Chroma appends to the last token is
// dropped: the diff pane composes its own line breaks.
func (h Highlighter) token(base lipgloss.Style, tok chroma.Token) string {
	value := strings.TrimSuffix(tok.Value, "\n")
	if value == "" {
		return ""
	}
	entry := h.style.Get(tok.Type)
	st := base
	if entry.Colour.IsSet() {
		st = st.Foreground(lipgloss.Color(entry.Colour.String()))
	}
	if entry.Bold == chroma.Yes {
		st = st.Bold(true)
	}
	if entry.Italic == chroma.Yes {
		st = st.Italic(true)
	}
	return st.Render(value)
}

// resolveStyle looks a Chroma style up by name, memoised, falling back to
// DefaultStyle and then to Chroma's own fallback so it never returns nil.
func resolveStyle(name string) *chroma.Style {
	if name == "" {
		name = DefaultStyle
	}
	if cached, ok := styleCache.Load(name); ok {
		return cached.(*chroma.Style)
	}
	s := styles.Get(name)
	if s == nil {
		s = styles.Fallback
	}
	styleCache.Store(name, s)
	return s
}

// StyleNames is every Chroma style the cockpit can be configured with, sorted.
// The settings panel validates against it so a typo fails at the field rather
// than silently falling back.
func StyleNames() []string { return styles.Names() }

// HasStyle reports whether name is a Chroma style wasa can resolve.
func HasStyle(name string) bool { return styles.Get(name) != nil }
