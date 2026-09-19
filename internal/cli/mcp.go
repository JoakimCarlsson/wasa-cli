package cli

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/joakimcarlsson/wasa-cli/internal/mcp"
	"github.com/joakimcarlsson/wasa-cli/internal/registry"
)

func init() {
	commands = append(commands, &Command{
		Name:    "mcp",
		Summary: "declare the MCP servers sessions launch their agent with",
		Run:     runMCP,
	})
}

const mcpUsage = "usage: wasa mcp <list|add|remove>"

func runMCP(args []string) error {
	if len(args) == 0 {
		return errors.New(mcpUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return mcpList(rest)
	case "add":
		return mcpAdd(rest)
	case "remove":
		return mcpRemove(rest)
	default:
		return fmt.Errorf("unknown mcp subcommand %q\n%s", sub, mcpUsage)
	}
}

const mcpListHelp = `usage: wasa mcp list [--profile <name>] [--json]

Show the MCP servers declared on the current repository's workspace profile —
the servers a session created from it hands its agent — followed by every
agent wasa knows and whether it can be handed them.

An agent with no MCP mechanism is listed as "not supported": sessions with that
agent launch normally and simply start without the declared servers.
`

func mcpList(args []string) error {
	fs := newFlagSet("wasa mcp list")
	profileName := mcpProfileFlag(fs)
	asJSON := jsonFlag(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, mcpListHelp)
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return errors.New(
			"usage: wasa mcp list [--profile <name>] [--json]",
		)
	}

	_, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}
	prof, err := current.SelectProfile(*profileName)
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(os.Stdout, mcpJSON{
			Profile: prof.Name,
			Servers: prof.MCPServers,
			Agents:  mcp.Agents(),
		})
	}

	tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	for _, name := range mcpNames(prof.MCPServers) {
		fmt.Fprintf(
			tw, "%s\t%s\n", name, mcpTarget(prof.MCPServers[name]),
		)
	}
	if len(prof.MCPServers) == 0 {
		fmt.Fprintf(tw, "no MCP servers declared on profile %s\n", prof.Name)
	}
	fmt.Fprintln(tw)
	for _, a := range mcp.Agents() {
		support := "not supported"
		if a.Supported {
			support = a.Path
		}
		fmt.Fprintf(tw, "%s\t%s\n", a.Exe, support)
	}
	return tw.Flush()
}

const mcpAddHelp = `usage: wasa mcp add [flags] <name> [--] <command> [args...]
       wasa mcp add [flags] --url <url> <name>

Declare an MCP server on the current repository's workspace profile. Every
session created from that profile afterwards writes it into the launched
agent's own MCP configuration inside the session worktree, so the agent starts
with the server available.

A local server is a command and its arguments; put "--" before the command when
any of its arguments start with a dash. A remote server is declared with --url
instead, and --type selects its transport (http by default). Remote servers are
written through verbatim, so an agent that spells remote transports its own way
may not pick them up; a command server is the interoperable shape.

Re-adding an existing name replaces its declaration.

Flags:
  --profile <name>   profile to declare on (default: the workspace default)
  --env KEY=VALUE    environment variable for the server; repeatable
  --url <url>        declare a remote server at this URL instead of a command
  --type <type>      remote transport: http or sse (default http)
`

func mcpAdd(args []string) error {
	fs := newFlagSet("wasa mcp add")
	profileName := mcpProfileFlag(fs)
	var env envFlag
	fs.Var(
		&env,
		"env",
		"environment variable for the server as KEY=VALUE; repeatable",
	)
	var url, transport string
	fs.StringVar(
		&url,
		"url",
		"",
		"declare a remote server at this URL instead of a command",
	)
	fs.StringVar(
		&transport,
		"type",
		"",
		"remote transport: http or sse (default http)",
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, mcpAddHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return errors.New(
			"usage: wasa mcp add [flags] <name> [--] <command> [args...]",
		)
	}
	name, command := rest[0], rest[1:]
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}

	server, err := mcpServer(
		url, transport, command, map[string]string(env),
	)
	if err != nil {
		return err
	}

	reg, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}
	prof, err := mcpProfile(current, *profileName)
	if err != nil {
		return err
	}
	if prof.MCPServers == nil {
		prof.MCPServers = map[string]registry.MCPServer{}
	}
	prof.MCPServers[name] = server
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stdout, "declared MCP server %s on profile %s\n", name, prof.Name,
	)
	return nil
}

const mcpRemoveHelp = `usage: wasa mcp remove [--profile <name>] <name>

Drop an MCP server declaration from the current repository's workspace profile.
Sessions already running keep the configuration written into their worktree;
sessions created afterwards launch without the server.
`

func mcpRemove(args []string) error {
	fs := newFlagSet("wasa mcp remove")
	profileName := mcpProfileFlag(fs)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(os.Stdout, mcpRemoveHelp)
			return nil
		}
		return err
	}

	rest := fs.Args()
	if len(rest) != 1 {
		return errors.New("usage: wasa mcp remove [--profile <name>] <name>")
	}
	name := rest[0]

	reg, current, err := openRegistry()
	if err != nil {
		return err
	}
	if current == nil {
		return errors.New("not a git repository")
	}
	prof, err := mcpProfile(current, *profileName)
	if err != nil {
		return err
	}
	if _, ok := prof.MCPServers[name]; !ok {
		return fmt.Errorf(
			"no MCP server %q declared on profile %s", name, prof.Name,
		)
	}
	delete(prof.MCPServers, name)
	if err := reg.Save(); err != nil {
		return err
	}

	fmt.Fprintf(
		os.Stdout, "removed MCP server %s from profile %s\n", name, prof.Name,
	)
	return nil
}

// mcpProfileFlag registers the uniform --profile selector every mcp
// subcommand takes, so list, add and remove all name a profile the same way.
func mcpProfileFlag(fs *flag.FlagSet) *string {
	return fs.String(
		"profile",
		"",
		"profile to act on (default: the workspace default profile)",
	)
}

// mcpProfile returns a pointer to the workspace's profile named name — the
// default profile when name is empty — so a caller mutates the stored profile
// rather than the copy SelectProfile hands back. An unknown name is an error,
// never a silent fall back to the default.
func mcpProfile(
	ws *registry.Workspace, name string,
) (*registry.Profile, error) {
	if name == "" {
		if len(ws.Profiles) == 0 {
			return nil, errors.New("workspace has no profiles")
		}
		return &ws.Profiles[0], nil
	}
	for i := range ws.Profiles {
		if ws.Profiles[i].Name == name {
			return &ws.Profiles[i], nil
		}
	}
	return nil, fmt.Errorf("unknown profile %q", name)
}

// mcpServer builds the declaration from the add command's arguments: a remote
// server from url and transport, or a local one from command and env. Exactly
// one of the two shapes must be given, so a half-declared server never reaches
// an agent's configuration.
func mcpServer(
	url, transport string,
	command []string,
	env map[string]string,
) (registry.MCPServer, error) {
	switch {
	case url != "" && len(command) > 0:
		return registry.MCPServer{}, errors.New(
			"a server is either --url or a command, not both",
		)
	case url != "":
		if transport == "" {
			transport = "http"
		}
		if transport != "http" && transport != "sse" {
			return registry.MCPServer{}, fmt.Errorf(
				"unknown transport %q: use http or sse", transport,
			)
		}
		return registry.MCPServer{
			Type: transport,
			URL:  url,
			Env:  env,
		}, nil
	case len(command) == 0:
		return registry.MCPServer{}, errors.New(
			"a command (or --url) is required",
		)
	case transport != "":
		return registry.MCPServer{}, errors.New(
			"--type applies to a --url server only",
		)
	default:
		return registry.MCPServer{
			Command: command[0],
			Args:    command[1:],
			Env:     env,
		}, nil
	}
}

// mcpNames returns the declared server names in sorted order, so listings are
// stable across runs of the same declaration.
func mcpNames(servers map[string]registry.MCPServer) []string {
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// mcpTarget renders a declaration's one-line target for the list command: its
// URL for a remote server, its command line for a local one.
func mcpTarget(s registry.MCPServer) string {
	if s.URL != "" {
		return s.Type + " " + s.URL
	}
	return strings.TrimSpace(s.Command + " " + strings.Join(s.Args, " "))
}

// envFlag collects repeated --env KEY=VALUE options into one map.
type envFlag map[string]string

// String renders the collected variables for flag's usage output, keys only so
// a value that carries a secret is never echoed.
func (e envFlag) String() string {
	keys := make([]string, 0, len(e))
	for k := range e {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return strings.Join(keys, ",")
}

// Set records one KEY=VALUE option, rejecting a spelling without '='.
func (e *envFlag) Set(v string) error {
	key, value, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(key) == "" {
		return fmt.Errorf("expected KEY=VALUE, got %q", v)
	}
	if *e == nil {
		*e = envFlag{}
	}
	(*e)[strings.TrimSpace(key)] = value
	return nil
}
