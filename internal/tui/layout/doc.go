// Package layout owns the cockpit's geometry: the spacing scale every pane
// indents and separates with, and the Frame that turns a terminal size into the
// column widths and body height the views draw into. It is a leaf package —
// config and nothing else — so every layer can size against the same frame
// rather than rediscovering it with a local subtraction.
package layout
