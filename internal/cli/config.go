package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"

	"github.com/joakimcarlsson/wasa-cli/internal/config"
)

func init() {
	commands = append(commands, &Command{
		Name:    "config",
		Summary: "show the cockpit configuration and its file path",
		Run:     runConfig,
	})
}

const configUsage = "usage: wasa config <path|show>"

func runConfig(args []string) error {
	if len(args) == 0 {
		return errors.New(configUsage)
	}

	sub, rest := args[0], args[1:]
	switch sub {
	case "path":
		return configPath(rest)
	case "show":
		return configShow(rest)
	default:
		return fmt.Errorf("unknown config subcommand %q\n%s", sub, configUsage)
	}
}

// configPath prints the resolved config file location. It reports the path the
// cockpit would read whether or not the file exists, so a user knows where to
// create it.
func configPath(args []string) error {
	if len(args) != 0 {
		return errors.New("usage: wasa config path")
	}
	cfg, err := config.Load(wasaHome())
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, cfg.Path)
	return nil
}

// configShow prints the effective configuration as JSON, preceded by the file
// path it was resolved from. With no user file the output is the built-in
// defaults, which doubles as a template a user can copy into config.json.
func configShow(args []string) error {
	fs := newFlagSet("wasa config show")
	asJSON := jsonFlag(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return errors.New("usage: wasa config show [--json]")
	}
	cfg, err := config.Load(wasaHome())
	if err != nil {
		return err
	}

	if *asJSON {
		return emitJSON(os.Stdout, cfg)
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	fmt.Fprintf(os.Stdout, "# %s\n%s\n", cfg.Path, data)
	return nil
}
