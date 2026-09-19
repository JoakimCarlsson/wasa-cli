package theme

import (
	"image/color"
	"reflect"
	"testing"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/compat"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

// TestDefaultThemeIsDefaultPalette pins the resolved default styles to the
// shipped palette, so the zero-config cockpit keeps its exact appearance.
func TestDefaultThemeIsDefaultPalette(t *testing.T) {
	th := NewTheme(config.Default().Theme)

	wantAccent := compat.AdaptiveColor{
		Light: lipgloss.Color("#0969DA"),
		Dark:  lipgloss.Color("#4493F8"),
	}
	if got := th.TitleStyle.GetForeground(); !reflect.DeepEqual(
		got,
		wantAccent,
	) {
		t.Errorf("accent: got %v", got)
	}
	wantRunning := compat.AdaptiveColor{
		Light: lipgloss.Color("#1A7F37"),
		Dark:  lipgloss.Color("#3FB950"),
	}
	if got := th.RunningDotStyle.GetForeground(); !reflect.DeepEqual(
		got,
		wantRunning,
	) {
		t.Errorf("running dot: got %v", got)
	}
	if got := th.ActiveTabStyle.GetForeground(); got != lipgloss.Color(
		"#FFFFFF",
	) {
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
	for name, got := range map[string]color.Color{
		"title":   th.TitleStyle.GetForeground(),
		"paneTab": th.PaneTabActiveStyle.GetForeground(),
		"banner":  th.BannerStyle.GetForeground(),
		"selBg":   th.ActiveTabStyle.GetBackground(),
		"matched": th.MatchStyle.GetForeground(),
	} {
		if got != want {
			t.Errorf("%s not recoloured by accent override: got %v", name, got)
		}
	}
}
