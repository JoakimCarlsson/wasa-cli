package tui

import (
	"github.com/joakimcarlsson/wasa/internal/config"
	"github.com/joakimcarlsson/wasa/internal/tui/component"
)

// testTheme is the resolved default theme, used by the bespoke-component tests
// that build a form or editor directly rather than through New.
func testTheme() component.Theme {
	return component.NewTheme(config.Default().Theme)
}
