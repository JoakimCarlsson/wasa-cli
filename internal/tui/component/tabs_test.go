package component

import (
	"strings"
	"testing"

	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/tui/theme"
)

func tabsTheme() theme.Theme {
	return theme.NewTheme(config.Default().Theme)
}

// TestTabStripRendersAllLabels checks the strip renders every label it is given,
// each in its own bordered box, so the row carries one segment per tab.
func TestTabStripRendersAllLabels(t *testing.T) {
	labels := []string{"Preview", "Diff", "Terminal"}
	out := TabStrip(tabsTheme(), labels, 0, 60)

	for _, label := range labels {
		if !strings.Contains(out, label) {
			t.Fatalf("strip missing label %q:\n%s", label, out)
		}
	}

	lines := strings.Split(out, "\n")
	if len(lines) < 3 {
		t.Fatalf(
			"tab strip should render as a multi-line bordered row, got:\n%s",
			out,
		)
	}
}

// TestTabStripActiveDiffersFromInactive checks the rendered strip changes when a
// different tab is active, so the active tab is distinguished structurally from
// the inactive ones (its border opens into the window beneath).
func TestTabStripActiveDiffersFromInactive(t *testing.T) {
	labels := []string{"Preview", "Diff", "Terminal"}
	th := tabsTheme()

	first := TabStrip(th, labels, 0, 60)
	second := TabStrip(th, labels, 1, 60)
	if first == second {
		t.Fatal("strip render did not change when the active tab moved")
	}
}
