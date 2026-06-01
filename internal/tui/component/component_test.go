package component

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

func TestOverlayKeepsBackgroundAndForeground(t *testing.T) {
	bg := strings.Join([]string{
		"aaaaaaaaaa",
		"aaaaaaaaaa",
		"aaaaaaaaaa",
	}, "\n")
	fg := "FG"

	out := Overlay(fg, bg)
	if !strings.Contains(out, "FG") {
		t.Fatalf("overlay dropped the foreground:\n%s", out)
	}
	if !strings.Contains(out, "a") {
		t.Fatalf("overlay cleared the background instead of floating over it:\n%s", out)
	}
	if got := strings.Count(out, "\n"); got != 2 {
		t.Fatalf("overlay changed the background height: %d newlines, want 2", got)
	}
}

func TestTabsRendersEveryLabel(t *testing.T) {
	tabs := Tabs{
		Names:         []string{"Preview", "Diff", "Terminal"},
		Active:        1,
		ActiveStyle:   lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
		InactiveStyle: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()),
	}
	out := tabs.Render(60)
	for _, name := range tabs.Names {
		if !strings.Contains(out, name) {
			t.Fatalf("tab strip missing %q:\n%s", name, out)
		}
	}
}

func TestTabsEmptyIsBlank(t *testing.T) {
	if got := (Tabs{}).Render(40); got != "" {
		t.Fatalf("empty tabs rendered %q, want blank", got)
	}
}
