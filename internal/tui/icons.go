package tui

// The glyphs the list and menu render: a branch marker, the per-status dots
// (running, waiting, idle, exited, paused), and the menu-bar separator. They
// live with the app render code (statusDot, sessionRow, menuBar) that draws
// them rather than with the theme, which only colours them.
const (
	branchIcon    = "Ꮧ"
	runningIcon   = "●"
	waitingIcon   = "◆"
	idleIcon      = "○"
	exitedIcon    = "●"
	pausedIcon    = "◫"
	recordIcon    = "⏺"
	collisionIcon = "⚠"
	menuSep       = " • "
)
