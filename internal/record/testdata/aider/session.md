# aider chat started at 2026-07-05 14:40:00

#### Add a --version flag to the CLI.

I'll add a `--version` flag that prints the build version.

```go
func addVersionFlag(fs *flag.FlagSet) {
	fs.Bool("version", false, "print version and exit")
}
```

> Applied edit to cli.go
> Commit 1a2b3c4 feat: add --version flag

#### Now document it in the README.

Added a Usage note describing the --version flag.
