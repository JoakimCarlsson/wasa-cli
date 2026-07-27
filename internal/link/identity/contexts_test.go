//go:build !windows

package identity

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
)

func storePath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), ContextsFileName)
}

// TestLoadMissingFileIsEmpty covers the fresh user: no login yet is a state,
// not a failure.
func TestLoadMissingFileIsEmpty(t *testing.T) {
	s := NewContextStore(storePath(t))

	c, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Contexts) != 0 || c.CurrentContext != "" {
		t.Fatalf("expected empty document, got %+v", c)
	}
	if _, ok := c.Current(); ok {
		t.Fatal("empty document reported a current context")
	}
}

func TestRoundTrip(t *testing.T) {
	path := storePath(t)
	s := NewContextStore(path)

	err := s.Update(func(c *Contexts) error {
		c.Put(Context{
			Name:         "personal",
			CoreURL:      "https://core.wasa.dev",
			Principal:    "joakim",
			KeychainSlot: "personal",
		})
		c.Put(Context{
			Name:         "work",
			CoreURL:      "https://core.acme.internal",
			Principal:    "joakim@acme",
			KeychainSlot: "work",
		})
		return c.SetCurrent("work")
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	c, err := NewContextStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Contexts) != 2 {
		t.Fatalf("expected 2 contexts, got %d", len(c.Contexts))
	}
	cur, ok := c.Current()
	if !ok {
		t.Fatal("current context missing after save")
	}
	if cur.Name != "work" || cur.Principal != "joakim@acme" {
		t.Fatalf("unexpected current context %+v", cur)
	}
}

// TestPutReplacesAndFirstBecomesCurrent covers re-login to a known context and
// the first login making itself usable without an extra "use" step.
func TestPutReplacesAndFirstBecomesCurrent(t *testing.T) {
	c := &Contexts{}
	c.Put(Context{Name: "personal", CoreURL: "https://one"})
	if c.CurrentContext != "personal" {
		t.Fatalf("first context did not become current: %q", c.CurrentContext)
	}

	c.Put(Context{Name: "personal", CoreURL: "https://two"})
	if len(c.Contexts) != 1 {
		t.Fatalf("Put duplicated a context: %+v", c.Contexts)
	}
	if got := c.Contexts[0].CoreURL; got != "https://two" {
		t.Fatalf("Put did not replace: core url %q", got)
	}

	c.Put(Context{Name: "work", CoreURL: "https://three"})
	if c.CurrentContext != "personal" {
		t.Fatalf("Put moved current to %q", c.CurrentContext)
	}
}

func TestRemoveClearsDanglingCurrent(t *testing.T) {
	c := &Contexts{}
	c.Put(Context{Name: "personal"})

	if !c.Remove("personal") {
		t.Fatal("Remove reported nothing removed")
	}
	if c.CurrentContext != "" {
		t.Fatalf("current still points at %q", c.CurrentContext)
	}
	if c.Remove("personal") {
		t.Fatal("Remove reported a second removal")
	}
}

func TestSetCurrentUnknownFails(t *testing.T) {
	c := &Contexts{}
	if err := c.SetCurrent("ghost"); err == nil {
		t.Fatal("SetCurrent accepted an unknown context")
	}
}

// TestFileModeIs0600 checks the secret-adjacent invariant: the file naming the
// user's cores and principals is owner-only.
func TestFileModeIs0600(t *testing.T) {
	path := storePath(t)
	s := NewContextStore(path)

	err := s.Update(func(c *Contexts) error {
		c.Put(Context{Name: "personal"})
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("mode is %#o, want 0600", perm)
	}
}

// TestCorruptFileSelfHeals checks a truncated write does not wedge every later
// command: the document reads as empty and the next save repairs it.
func TestCorruptFileSelfHeals(t *testing.T) {
	path := storePath(t)
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("seed: %v", err)
	}
	s := NewContextStore(path)

	c, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Contexts) != 0 {
		t.Fatalf("expected empty document, got %+v", c)
	}

	err = s.Update(func(c *Contexts) error {
		c.Put(Context{Name: "personal"})
		return nil
	})
	if err != nil {
		t.Fatalf("Update: %v", err)
	}
	if c, err = s.Load(); err != nil || len(c.Contexts) != 1 {
		t.Fatalf("store did not heal: %+v (%v)", c, err)
	}
}

// TestUpdateErrorAborts checks a failed mutation leaves the file untouched
// rather than half-written.
func TestUpdateErrorAborts(t *testing.T) {
	path := storePath(t)
	s := NewContextStore(path)

	want := fmt.Errorf("boom")
	err := s.Update(func(c *Contexts) error {
		c.Put(Context{Name: "personal"})
		return want
	})
	if err != want {
		t.Fatalf("Update returned %v, want %v", err, want)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file was written despite the error (%v)", err)
	}
}

// TestConcurrentUpdatesDoNotClobber is the flock test: many writers doing a
// read-modify-write on the same document must all survive, which only holds if
// the lock spans both the read and the write.
func TestConcurrentUpdatesDoNotClobber(t *testing.T) {
	path := storePath(t)

	const writers = 12
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := NewContextStore(path)
			errs <- s.Update(func(c *Contexts) error {
				c.Put(Context{
					Name:         fmt.Sprintf("ctx-%02d", i),
					KeychainSlot: fmt.Sprintf("slot-%02d", i),
				})
				return nil
			})
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Update: %v", err)
		}
	}

	c, err := NewContextStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Contexts) != writers {
		t.Fatalf("kept %d of %d contexts", len(c.Contexts), writers)
	}
	for i := range writers {
		name := fmt.Sprintf("ctx-%02d", i)
		if _, ok := c.Find(name); !ok {
			t.Errorf("context %s was clobbered", name)
		}
	}
}

const (
	helperPathEnv = "WASA_TEST_CONTEXTS_PATH"
	helperNameEnv = "WASA_TEST_CONTEXT_NAME"
)

// TestHelperProcessWriteContext is not a test: it is the body of the child
// processes TestConcurrentProcessesDoNotClobber spawns.
func TestHelperProcessWriteContext(t *testing.T) {
	path := os.Getenv(helperPathEnv)
	if path == "" {
		t.Skip("not running as a helper process")
	}
	name := os.Getenv(helperNameEnv)
	s := NewContextStore(path)
	err := s.Update(func(c *Contexts) error {
		c.Put(Context{Name: name, KeychainSlot: name})
		return nil
	})
	if err != nil {
		t.Fatalf("helper Update: %v", err)
	}
}

// TestConcurrentProcessesDoNotClobber is the invariant flock exists for: a
// foreground command and a background push are separate processes, so an
// in-process mutex would not save them. Each child does its own
// read-modify-write on one document and all of them must survive.
func TestConcurrentProcessesDoNotClobber(t *testing.T) {
	if testing.Short() {
		t.Skip("spawns child processes")
	}
	path := storePath(t)

	const writers = 8
	cmds := make([]*exec.Cmd, 0, writers)
	for i := range writers {
		cmd := exec.Command(
			os.Args[0],
			"-test.run=^TestHelperProcessWriteContext$",
		)
		cmd.Env = append(
			os.Environ(),
			helperPathEnv+"="+path,
			helperNameEnv+"="+fmt.Sprintf("ctx-%02d", i),
		)
		if err := cmd.Start(); err != nil {
			t.Fatalf("start helper %d: %v", i, err)
		}
		cmds = append(cmds, cmd)
	}
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v", i, err)
		}
	}

	c, err := NewContextStore(path).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if len(c.Contexts) != writers {
		t.Fatalf("kept %d of %d contexts", len(c.Contexts), writers)
	}
}
