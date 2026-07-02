// Package theme holds the cockpit's resolved lipgloss styles. It is a standalone
// leaf package so the component, pane, modal, and root tui layers can all import
// the Theme type without an import cycle. It depends only on config and lipgloss.
package theme
