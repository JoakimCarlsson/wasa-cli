package tui

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// testTheme is the resolved default palette, built once for the component tests
// that need a theme value to construct against. It replaces the old global
// styles the package used to mutate, so tests build their own theme and never
// disturb one another.
var testTheme = newTheme(config.Default().Theme)

// TestDefaultThemeIsHistoricalPalette pins the resolved default styles to the
// historical colours, so the zero-config cockpit keeps its exact appearance.
func TestDefaultThemeIsHistoricalPalette(t *testing.T) {
	th := newTheme(config.Default().Theme)

	if got := th.titleStyle.GetForeground(); got != (lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}) {
		t.Errorf("accent: got %v", got)
	}
	if got := th.runningDotStyle.GetForeground(); got != lipgloss.Color(
		"#51bd73",
	) {
		t.Errorf("running dot: got %v", got)
	}
	if got := th.activeTabStyle.GetForeground(); got != lipgloss.Color("230") {
		t.Errorf("active tab fg: got %v", got)
	}
}

// TestNewThemeOverridesAccent confirms an accent override recolours the
// accent-driven styles in the theme it builds.
func TestNewThemeOverridesAccent(t *testing.T) {
	cfg := config.Default().Theme
	cfg.Accent = config.Color{Light: "#abcdef", Dark: "#abcdef"}
	th := newTheme(cfg)

	want := lipgloss.Color("#abcdef")
	for name, got := range map[string]lipgloss.TerminalColor{
		"title":   th.titleStyle.GetForeground(),
		"pane":    th.paneStyle.GetBorderTopForeground(),
		"banner":  th.bannerStyle.GetForeground(),
		"selBg":   th.activeTabStyle.GetBackground(),
		"matched": th.matchStyle.GetForeground(),
	} {
		if got != want {
			t.Errorf("%s not recoloured by accent override: got %v", name, got)
		}
	}
}
