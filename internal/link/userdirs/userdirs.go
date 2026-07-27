//go:build !windows

package userdirs

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

const appName = "wasa"

// Config returns the directory holding wasa's global configuration, creating
// it if needed. It is $XDG_CONFIG_HOME/wasa when that variable is set and
// $HOME/.config/wasa otherwise.
func Config() (string, error) {
	return resolve("config", "XDG_CONFIG_HOME", ".config")
}

// Cache returns the directory holding wasa's global cache, creating it if
// needed. It is $XDG_CACHE_HOME/wasa when that variable is set and
// $HOME/.cache/wasa otherwise.
func Cache() (string, error) {
	return resolve("cache", "XDG_CACHE_HOME", ".cache")
}

func resolve(kind, env, homeDir string) (string, error) {
	dir, err := base(kind, env, homeDir)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}

func base(kind, env, homeDir string) (string, error) {
	if testing.Testing() {
		root, err := testRoot()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, kind), nil
	}
	if v := os.Getenv(env); v != "" {
		return filepath.Join(v, appName), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, homeDir, appName), nil
}

var (
	testRootOnce sync.Once
	testRootDir  string
	testRootErr  error
)

func testRoot() (string, error) {
	testRootOnce.Do(func() {
		testRootDir, testRootErr = os.MkdirTemp("", "wasa-userdirs-*")
	})
	return testRootDir, testRootErr
}
