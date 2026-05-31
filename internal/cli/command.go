package cli

// Command is a single wasa subcommand. Feature subcommands such as worktree,
// tmux, workspace and finish will register themselves by appending to commands.
type Command struct {
	// Name is the token typed on the command line to invoke the command.
	Name string
	// Summary is a one-line description shown in the top-level usage listing.
	Summary string
	// Hidden omits the command from the usage listing. It is for internal
	// subcommands wasa invokes on itself, such as the Windows session daemon and
	// attach relay, which users never type.
	Hidden bool
	// Run executes the command with its remaining arguments.
	Run func(args []string) error
}

// commands holds every registered subcommand. It is intentionally empty for
// now; the dispatch scaffolding in Run already routes to whatever is added.
var commands []*Command

// lookup returns the registered command with the given name, if any.
func lookup(name string) (*Command, bool) {
	for _, c := range commands {
		if c.Name == name {
			return c, true
		}
	}
	return nil, false
}
