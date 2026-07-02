// Package modal holds the cockpit's full-screen modal screens: the create form,
// the yes/no confirm dialog and the in-cockpit settings editor. Each owns only
// its presentation, focus and result reporting; the root tui package constructs
// them, routes input, and acts on the exported result messages each emits. The
// package may
// build on internal/tui/component but never imports the root tui package nor
// internal/tui/pane.
package modal
