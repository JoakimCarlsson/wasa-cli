//go:build !windows

package userdirs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestConfigAndCacheAreDistinct checks the resolver keeps the two roots apart:
// a cached node map must never land in the directory holding the login.
func TestConfigAndCacheAreDistinct(t *testing.T) {
	config, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	cache, err := Cache()
	if err != nil {
		t.Fatalf("Cache: %v", err)
	}
	if config == cache {
		t.Fatalf("config and cache resolved to the same dir %q", config)
	}
	for _, dir := range []string{config, cache} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat %q: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%q is not a directory", dir)
		}
	}
}

// TestUnderTestIsolatedFromRealHome is the guard the whole package exists for:
// a test run must not be able to reach the developer's real ~/.config/wasa.
func TestUnderTestIsolatedFromRealHome(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", filepath.Join("nonexistent", "xdg"))

	config, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if strings.Contains(config, "xdg") {
		t.Fatalf("XDG override leaked into test dir %q", config)
	}

	if !strings.HasPrefix(config, os.TempDir()) {
		t.Fatalf(
			"config dir %q is not under the temp root %q",
			config, os.TempDir(),
		)
	}
}

// TestStableAcrossCalls checks the per-process temp root is resolved once, so
// two stores opened at different times see the same state.
func TestStableAcrossCalls(t *testing.T) {
	first, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	second, err := Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if first != second {
		t.Fatalf("Config not stable: %q then %q", first, second)
	}
}
