//go:build windows

package conpty

import "strings"

// screen is a minimal terminal-screen emulator: it consumes the byte stream a
// pseudo-console emits and maintains the visible character grid so Capture can
// render a plain-text snapshot, mirroring tmux's "capture-pane -p". It is not a
// full terminal — colours and styling (SGR) are discarded, and it tracks only
// the cursor movement, erasing, scrolling and alternate-screen control sequences
// needed to keep the grid faithful enough for a read-only preview.
//
// A screen is fed incrementally; the parser state persists across Write calls
// because pseudo-console output arrives in arbitrarily-split chunks.
type screen struct {
	cols, rows int
	cells      [][]rune
	x, y       int

	state   parseState
	params  []byte
	private bool
}

type parseState int

const (
	stateGround parseState = iota
	stateEsc
	stateCSI
	stateOSC
	stateOSCEsc
	stateEscInter
)

func newScreen(cols, rows int) *screen {
	s := &screen{cols: cols, rows: rows}
	s.reset()
	return s
}

func (s *screen) reset() {
	s.cells = make([][]rune, s.rows)
	for i := range s.cells {
		s.cells[i] = blankRow(s.cols)
	}
	s.x, s.y = 0, 0
}

func blankRow(cols int) []rune {
	row := make([]rune, cols)
	for i := range row {
		row[i] = ' '
	}
	return row
}

// Resize re-shapes the grid to cols×rows, preserving overlapping content and
// clamping the cursor. A pseudo-console repaints after a resize, so exactness
// here is not load-bearing.
func (s *screen) Resize(cols, rows int) {
	if cols <= 0 || rows <= 0 || (cols == s.cols && rows == s.rows) {
		return
	}
	next := make([][]rune, rows)
	for i := range next {
		next[i] = blankRow(cols)
		if i < len(s.cells) {
			copy(next[i], s.cells[i])
		}
	}
	s.cells, s.cols, s.rows = next, cols, rows
	s.x = clamp(s.x, 0, cols-1)
	s.y = clamp(s.y, 0, rows-1)
}

// Write feeds output bytes through the parser, updating the grid.
func (s *screen) Write(p []byte) (int, error) {
	for _, b := range p {
		s.step(b)
	}
	return len(p), nil
}

func (s *screen) step(b byte) {
	switch s.state {
	case stateGround:
		s.ground(b)
	case stateEsc:
		s.esc(b)
	case stateEscInter:
		s.state = stateGround
	case stateCSI:
		s.csi(b)
	case stateOSC:
		s.osc(b)
	case stateOSCEsc:
		if b == '\\' {
			s.state = stateGround
		} else {
			s.state = stateOSC
		}
	}
}

func (s *screen) ground(b byte) {
	switch b {
	case 0x1b:
		s.state = stateEsc
	case '\r':
		s.x = 0
	case '\n':
		s.lineFeed()
	case '\b':
		if s.x > 0 {
			s.x--
		}
	case '\t':
		s.x = clamp((s.x/8+1)*8, 0, s.cols-1)
	case '\a', 0x00:
	default:
		if b >= 0x20 {
			s.put(rune(b))
		}
	}
}

func (s *screen) esc(b byte) {
	switch b {
	case '[':
		s.params = s.params[:0]
		s.private = false
		s.state = stateCSI
	case ']':
		s.state = stateOSC
	case '(', ')', '*', '+', '-', '.', '/', '#', ' ':
		s.state = stateEscInter
	case 'c':
		s.reset()
		s.state = stateGround
	default:
		s.state = stateGround
	}
}

func (s *screen) osc(b byte) {
	switch b {
	case '\a':
		s.state = stateGround
	case 0x1b:
		s.state = stateOSCEsc
	}
}

func (s *screen) csi(b byte) {
	switch {
	case b == '?':
		s.private = true
	case b >= 0x30 && b <= 0x3b:
		s.params = append(s.params, b)
	case b >= 0x40 && b <= 0x7e:
		s.dispatchCSI(b)
		s.state = stateGround
	}
}

func (s *screen) dispatchCSI(final byte) {
	args := parseParams(s.params)
	switch final {
	case 'A':
		s.y = clamp(s.y-arg(args, 0, 1), 0, s.rows-1)
	case 'B', 'e':
		s.y = clamp(s.y+arg(args, 0, 1), 0, s.rows-1)
	case 'C', 'a':
		s.x = clamp(s.x+arg(args, 0, 1), 0, s.cols-1)
	case 'D':
		s.x = clamp(s.x-arg(args, 0, 1), 0, s.cols-1)
	case 'E':
		s.x, s.y = 0, clamp(s.y+arg(args, 0, 1), 0, s.rows-1)
	case 'F':
		s.x, s.y = 0, clamp(s.y-arg(args, 0, 1), 0, s.rows-1)
	case 'G', '`':
		s.x = clamp(arg(args, 0, 1)-1, 0, s.cols-1)
	case 'd':
		s.y = clamp(arg(args, 0, 1)-1, 0, s.rows-1)
	case 'H', 'f':
		s.y = clamp(arg(args, 0, 1)-1, 0, s.rows-1)
		s.x = clamp(arg(args, 1, 1)-1, 0, s.cols-1)
	case 'J':
		s.eraseDisplay(arg(args, 0, 0))
	case 'K':
		s.eraseLine(arg(args, 0, 0))
	case 'X':
		s.eraseChars(arg(args, 0, 1))
	case 'P':
		s.deleteChars(arg(args, 0, 1))
	case '@':
		s.insertBlanks(arg(args, 0, 1))
	case 'h', 'l':
		s.privateMode(args, final == 'h')
	}
}

func (s *screen) privateMode(args []int, set bool) {
	if !s.private {
		return
	}
	for _, a := range args {
		if a == 1049 || a == 47 || a == 1047 {
			s.reset()
		}
	}
	_ = set
}

func (s *screen) lineFeed() {
	if s.y >= s.rows-1 {
		s.scrollUp()
	} else {
		s.y++
	}
}

func (s *screen) scrollUp() {
	copy(s.cells, s.cells[1:])
	s.cells[s.rows-1] = blankRow(s.cols)
}

func (s *screen) put(r rune) {
	if s.x >= s.cols {
		s.x = 0
		s.lineFeed()
	}
	s.cells[s.y][s.x] = r
	s.x++
}

func (s *screen) eraseDisplay(mode int) {
	switch mode {
	case 0:
		s.eraseLine(0)
		for y := s.y + 1; y < s.rows; y++ {
			s.cells[y] = blankRow(s.cols)
		}
	case 1:
		s.eraseLine(1)
		for y := range s.y {
			s.cells[y] = blankRow(s.cols)
		}
	case 2, 3:
		for y := range s.rows {
			s.cells[y] = blankRow(s.cols)
		}
	}
}

func (s *screen) eraseLine(mode int) {
	row := s.cells[s.y]
	switch mode {
	case 0:
		for x := s.x; x < s.cols; x++ {
			row[x] = ' '
		}
	case 1:
		for x := 0; x <= s.x && x < s.cols; x++ {
			row[x] = ' '
		}
	case 2:
		s.cells[s.y] = blankRow(s.cols)
	}
}

func (s *screen) eraseChars(n int) {
	row := s.cells[s.y]
	for x := s.x; x < s.x+n && x < s.cols; x++ {
		row[x] = ' '
	}
}

func (s *screen) deleteChars(n int) {
	row := s.cells[s.y]
	copy(row[s.x:], row[s.x+min(n, s.cols-s.x):])
	for x := s.cols - n; x < s.cols; x++ {
		if x >= 0 {
			row[x] = ' '
		}
	}
}

func (s *screen) insertBlanks(n int) {
	row := s.cells[s.y]
	if s.x+n < s.cols {
		copy(row[s.x+n:], row[s.x:])
	}
	for x := s.x; x < s.x+n && x < s.cols; x++ {
		row[x] = ' '
	}
}

// Snapshot renders the grid to plain text: trailing spaces on each line and
// trailing blank lines are trimmed, matching the shape of "capture-pane -p".
func (s *screen) Snapshot() string {
	lines := make([]string, s.rows)
	for y, row := range s.cells {
		lines[y] = strings.TrimRight(string(row), " ")
	}
	end := len(lines)
	for end > 0 && lines[end-1] == "" {
		end--
	}
	return strings.Join(lines[:end], "\n")
}

func parseParams(raw []byte) []int {
	if len(raw) == 0 {
		return nil
	}
	var out []int
	for part := range strings.SplitSeq(string(raw), ";") {
		n := 0
		for _, c := range part {
			if c < '0' || c > '9' {
				n = 0
				break
			}
			n = n*10 + int(c-'0')
		}
		out = append(out, n)
	}
	return out
}

func arg(args []int, i, def int) int {
	if i < len(args) && args[i] != 0 {
		return args[i]
	}
	return def
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
