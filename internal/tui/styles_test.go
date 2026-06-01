package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// TestDefaultThemeIsHistoricalPalette pins the resolved default styles to the
// historical colours, so the zero-config cockpit keeps its exact appearance.
func TestDefaultThemeIsHistoricalPalette(t *testing.T) {
	applyTheme(config.Default().Theme)

	if got := titleStyle.GetForeground(); got != (lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}) {
		t.Errorf("accent: got %v", got)
	}
	if got := runningDotStyle.GetForeground(); got != lipgloss.Color("#51bd73") {
		t.Errorf("running dot: got %v", got)
	}
	if got := activeTabStyle.GetForeground(); got != lipgloss.Color("230") {
		t.Errorf("active tab fg: got %v", got)
	}
}

// TestApplyThemeOverridesAccent confirms a theme override recolours the
// accent-driven styles. It restores the default theme so later tests see the
// historical palette.
func TestApplyThemeOverridesAccent(t *testing.T) {
	defer applyTheme(config.Default().Theme)

	th := config.Default().Theme
	th.Accent = config.Color{Light: "#abcdef", Dark: "#abcdef"}
	applyTheme(th)

	want := lipgloss.Color("#abcdef")
	for name, got := range map[string]lipgloss.TerminalColor{
		"title":   titleStyle.GetForeground(),
		"pane":    paneStyle.GetBorderTopForeground(),
		"banner":  bannerStyle.GetForeground(),
		"selBg":   activeTabStyle.GetBackground(),
		"matched": matchStyle.GetForeground(),
	} {
		if got != want {
			t.Errorf("%s not recoloured by accent override: got %v", name, got)
		}
	}
}
