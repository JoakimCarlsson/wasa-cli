package component

import (
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/joakimcarlsson/wasa/internal/config"
)

// TestDefaultThemeIsHistoricalPalette pins the resolved default styles to the
// historical colours, so the zero-config cockpit keeps its exact appearance.
func TestDefaultThemeIsHistoricalPalette(t *testing.T) {
	th := NewTheme(config.Default().Theme)

	if got := th.TitleStyle.GetForeground(); got != (lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}) {
		t.Errorf("accent: got %v", got)
	}
	if got := th.RunningDotStyle.GetForeground(); got != lipgloss.Color(
		"#51bd73",
	) {
		t.Errorf("running dot: got %v", got)
	}
	if got := th.ActiveTabStyle.GetForeground(); got != lipgloss.Color("230") {
		t.Errorf("active tab fg: got %v", got)
	}
}

// TestNewThemeOverridesAccent confirms a theme override recolours the
// accent-driven styles.
func TestNewThemeOverridesAccent(t *testing.T) {
	cfg := config.Default().Theme
	cfg.Accent = config.Color{Light: "#abcdef", Dark: "#abcdef"}
	th := NewTheme(cfg)

	want := lipgloss.Color("#abcdef")
	for name, got := range map[string]lipgloss.TerminalColor{
		"title":   th.TitleStyle.GetForeground(),
		"pane":    th.PaneStyle.GetBorderTopForeground(),
		"banner":  th.BannerStyle.GetForeground(),
		"selBg":   th.ActiveTabStyle.GetBackground(),
		"matched": th.MatchStyle.GetForeground(),
	} {
		if got != want {
			t.Errorf("%s not recoloured by accent override: got %v", name, got)
		}
	}
}
