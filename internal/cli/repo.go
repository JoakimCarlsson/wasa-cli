package cli

import (
	"os"
	"path/filepath"

	"github.com/joakimcarlsson/wasa/internal/registry"
	"github.com/joakimcarlsson/wasa/internal/repo"
)

// currentRepo resolves the git repository containing the working directory and
// returns its canonical absolute path and primary remote URL. The path is the
// stable identity passed to the registry; the remote URL is empty when the
// repository has no remote.
func currentRepo() (repoPath, remoteURL string, err error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", "", err
	}
	return resolveRepo(cwd)
}

// resolveRepo resolves the git repository containing dir to its canonical path
// and primary remote URL. It delegates to the shared repo package so the in-repo
// launch, workspace add and the cockpit create flow all derive identical
// workspace identities.
func resolveRepo(dir string) (repoPath, remoteURL string, err error) {
	return repo.Resolve(dir)
}

// registerRepo registers the repository identified by repoPath and remoteURL in
// reg, returning its workspace and whether it was newly created. It delegates to
// the shared repo package, the single registration code path across wasa.
func registerRepo(
	reg *registry.Registry,
	repoPath, remoteURL string,
) (*registry.Workspace, bool) {
	return repo.Register(reg, repoPath, remoteURL)
}

func wasaHome() string {
	if h := os.Getenv("WASA_HOME"); h != "" {
		return h
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".wasa"
	}
	return filepath.Join(home, ".wasa")
}
