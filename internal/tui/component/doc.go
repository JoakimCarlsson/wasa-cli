// Package component holds the generic, app-agnostic building blocks of the wasa
// cockpit: the resolved Theme of lipgloss styles, the Keymap that resolves key
// presses to actions, the directory and branch pickers, the connected tab-strip
// renderer, and the text/overlay helpers they share. Nothing here knows about
// the cockpit's registry, sessions or workspaces — the root tui package wires
// these pieces together — so the package depends only on config, lipgloss and
// the bubbles widgets, never on the root package.
package component
