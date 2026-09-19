package component

import (
	"strings"

	"github.com/charmbracelet/x/ansi"

	"github.com/joakimcarlsson/wasa-cli/internal/tui/theme"
)

// TabStrip renders a flat row of tab labels spanning totalWidth, under a faint
// rule: the active label is accented and underlined, the rest are muted. There
// are no boxes and no connecting border work — the strip sits directly on the
// terminal background, so the pane beneath it reads as part of the shell
// rather than as a window drawn inside it. active is the index of the open tab.
func TabStrip(t theme.Theme, labels []string, active, totalWidth int) string {
	var row strings.Builder
	for i, name := range labels {
		style := t.PaneTabInactiveStyle
		if i == active {
			style = t.PaneTabActiveStyle
		}
		row.WriteString(style.Render(name))
	}
	line := row.String()
	if pad := totalWidth - ansi.StringWidth(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	return line + "\n" + t.RuleStyle.Render(
		strings.Repeat("─", max(totalWidth, 0)),
	)
}
