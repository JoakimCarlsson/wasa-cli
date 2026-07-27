//go:build !windows

package identity

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"

	"github.com/joakimcarlsson/wasa-cli/internal/link/userdirs"
)

// ContextsFileName is the name of the context store inside the wasa config
// directory.
const ContextsFileName = "contexts.json"

// ErrNoContext is returned when an operation needs an active context and
// current_context is unset or dangling — the user is not linked or not logged
// in.
var ErrNoContext = errors.New("identity: no current context")

// Context is one named login: the core it talks to, the principal it
// authenticates as, and the keychain slot holding that identity's tokens.
type Context struct {
	Name         string `json:"name"`
	CoreURL      string `json:"core_url"`
	Principal    string `json:"principal"`
	KeychainSlot string `json:"keychain_slot"`
}

// Contexts is the on-disk document: every known context plus the pointer to
// the active one.
type Contexts struct {
	Contexts       []Context `json:"contexts"`
	CurrentContext string    `json:"current_context"`
}

// Find returns the context with the given name.
func (c *Contexts) Find(name string) (Context, bool) {
	i := slices.IndexFunc(c.Contexts, func(x Context) bool {
		return x.Name == name
	})
	if i < 0 {
		return Context{}, false
	}
	return c.Contexts[i], true
}

// Current returns the context current_context points at. A missing or dangling
// pointer reports false rather than an arbitrary context.
func (c *Contexts) Current() (Context, bool) {
	if c.CurrentContext == "" {
		return Context{}, false
	}
	return c.Find(c.CurrentContext)
}

// Put inserts ctx, replacing any context of the same name. It leaves
// current_context alone; the first context added becomes current so a fresh
// login is immediately usable.
func (c *Contexts) Put(ctx Context) {
	i := slices.IndexFunc(c.Contexts, func(x Context) bool {
		return x.Name == ctx.Name
	})
	if i < 0 {
		c.Contexts = append(c.Contexts, ctx)
	} else {
		c.Contexts[i] = ctx
	}
	if c.CurrentContext == "" {
		c.CurrentContext = ctx.Name
	}
}

// Remove deletes the named context, clearing current_context when it pointed
// at it. It reports whether anything was removed.
func (c *Contexts) Remove(name string) bool {
	i := slices.IndexFunc(c.Contexts, func(x Context) bool {
		return x.Name == name
	})
	if i < 0 {
		return false
	}
	c.Contexts = slices.Delete(c.Contexts, i, i+1)
	if c.CurrentContext == name {
		c.CurrentContext = ""
	}
	return true
}

// SetCurrent points current_context at an existing context.
func (c *Contexts) SetCurrent(name string) error {
	if _, ok := c.Find(name); !ok {
		return fmt.Errorf("identity: unknown context %q", name)
	}
	c.CurrentContext = name
	return nil
}

// ContextStore reads and writes the context document at a fixed path. Every
// operation takes an exclusive lock for its whole duration, so a foreground
// command and a background push never interleave a read-modify-write.
type ContextStore struct {
	path string
}

// NewContextStore returns a store backed by the document at path.
func NewContextStore(path string) *ContextStore {
	return &ContextStore{path: path}
}

// DefaultContextStore returns the store at contexts.json inside wasa's config
// directory — the one file the CLI and the git-remote helper share.
func DefaultContextStore() (*ContextStore, error) {
	dir, err := userdirs.Config()
	if err != nil {
		return nil, err
	}
	return NewContextStore(filepath.Join(dir, ContextsFileName)), nil
}

// Path returns the document's location.
func (s *ContextStore) Path() string { return s.path }

// Load reads the document. A missing file yields an empty document and no
// error: that is a fresh user, not a failure.
func (s *ContextStore) Load() (*Contexts, error) {
	lock, err := lockState(s.path)
	if err != nil {
		return nil, err
	}
	defer lock.unlock()

	return s.load()
}

// Save replaces the document with c.
func (s *ContextStore) Save(c *Contexts) error {
	lock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer lock.unlock()

	return s.save(c)
}

// Update applies mutate to the current document and writes the result back,
// holding one lock across both halves. Concurrent writers therefore serialise
// instead of clobbering each other's view.
func (s *ContextStore) Update(mutate func(*Contexts) error) error {
	lock, err := lockState(s.path)
	if err != nil {
		return err
	}
	defer lock.unlock()

	c, err := s.load()
	if err != nil {
		return err
	}
	if err := mutate(c); err != nil {
		return err
	}
	return s.save(c)
}

func (s *ContextStore) load() (*Contexts, error) {
	c := &Contexts{Contexts: []Context{}}
	if _, err := readJSON(s.path, c); err != nil {
		return nil, err
	}
	if c.Contexts == nil {
		c.Contexts = []Context{}
	}
	return c, nil
}

func (s *ContextStore) save(c *Contexts) error {
	doc := *c
	if doc.Contexts == nil {
		doc.Contexts = []Context{}
	}
	return writeJSON(s.path, doc)
}
