package tui

import "github.com/charmbracelet/x/ansi"

func viewContent(m Model) string {
	return m.View().Content
}

func plainViewContent(m Model) string {
	return ansi.Strip(viewContent(m))
}
