package component

import (
	"os"
	"strings"
)

// PickerRows is the most tree or list rows a picker shows at once; the view
// scrolls a window of this height over the visible entries. MaxRecents caps the
// directory picker's recent list.
const (
	PickerRows = 14
	MaxRecents = 8
)

// HomeRel rewrites a path under home to a ~-prefixed form for display, leaving
// paths outside home untouched.
func HomeRel(path, home string) string {
	if home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rel, ok := strings.CutPrefix(path, home+string(os.PathSeparator)); ok {
		return "~" + string(os.PathSeparator) + rel
	}
	return path
}

// fuzzyScore matches query against target as a case-insensitive subsequence,
// returning the match score, the byte positions in target that matched, and
// whether every query character was found in order. Consecutive matches and
// matches at a word boundary score higher. An empty query matches everything.
func fuzzyScore(query, target string) (int, []int, bool) {
	if query == "" {
		return 0, nil, true
	}
	q := strings.ToLower(query)
	t := strings.ToLower(target)

	positions := make([]int, 0, len(q))
	score, ti, prev := 0, 0, -2
	for qi := range len(q) {
		idx := strings.IndexByte(t[ti:], q[qi])
		if idx < 0 {
			return 0, nil, false
		}
		pos := ti + idx
		score++
		if pos == prev+1 {
			score += 5
		}
		if pos == 0 || isBoundary(target[pos-1]) {
			score += 10
		}
		positions = append(positions, pos)
		prev = pos
		ti = pos + 1
	}
	return score, positions, true
}

func isBoundary(b byte) bool {
	return b == '-' || b == '_' || b == ' ' || b == os.PathSeparator
}

// highlight styles label rune by rune, accenting the byte offsets listed in
// positions and leaving the rest plain, so a fuzzy match reads as the query
// characters lit up inside the name.
func highlight(theme Theme, label string, positions []int) string {
	if len(positions) == 0 {
		return label
	}
	set := make(map[int]bool, len(positions))
	for _, pos := range positions {
		set[pos] = true
	}
	var b strings.Builder
	for i, r := range label {
		if set[i] {
			b.WriteString(theme.MatchStyle.Render(string(r)))
		} else {
			b.WriteString(string(r))
		}
	}
	return b.String()
}
