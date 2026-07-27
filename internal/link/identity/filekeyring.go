//go:build !windows

package identity

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/joakimcarlsson/wasa-cli/internal/link/userdirs"
	"github.com/zalando/go-keyring"
)

// CredentialsFileName is the name of the fallback secret file inside the wasa
// config directory.
const CredentialsFileName = "credentials.json"

// FileKeyring is the fallback secret store used on hosts with no OS keychain.
// It is a `{ "service": { "user": "password" } }` JSON document at mode 0600,
// written temp+rename under an exclusive lock like every other state file.
type FileKeyring struct {
	path string
	warn io.Writer
}

// NewFileKeyring returns a file-backed keyring at path. Permission warnings go
// to w; a nil w silences them.
func NewFileKeyring(path string, w io.Writer) *FileKeyring {
	return &FileKeyring{path: path, warn: w}
}

// DefaultFileKeyring returns the fallback store in wasa's config directory,
// warning on stderr when the file's permissions are looser than 0600.
func DefaultFileKeyring() (*FileKeyring, error) {
	dir, err := userdirs.Config()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, CredentialsFileName)
	return NewFileKeyring(path, os.Stderr), nil
}

// Path returns the document's location.
func (k *FileKeyring) Path() string { return k.path }

// Get returns the secret stored for service and user, or keyring.ErrNotFound.
func (k *FileKeyring) Get(service, user string) (string, error) {
	lock, err := lockState(k.path)
	if err != nil {
		return "", err
	}
	defer lock.unlock()

	doc, err := k.load()
	if err != nil {
		return "", err
	}
	secret, ok := doc[service][user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return secret, nil
}

// Set stores a secret for service and user.
func (k *FileKeyring) Set(service, user, password string) error {
	lock, err := lockState(k.path)
	if err != nil {
		return err
	}
	defer lock.unlock()

	doc, err := k.load()
	if err != nil {
		return err
	}
	if doc[service] == nil {
		doc[service] = map[string]string{}
	}
	doc[service][user] = password
	return writeJSON(k.path, doc)
}

// Delete removes the secret for service and user, reporting
// keyring.ErrNotFound when there was none.
func (k *FileKeyring) Delete(service, user string) error {
	lock, err := lockState(k.path)
	if err != nil {
		return err
	}
	defer lock.unlock()

	doc, err := k.load()
	if err != nil {
		return err
	}
	if _, ok := doc[service][user]; !ok {
		return keyring.ErrNotFound
	}
	delete(doc[service], user)
	if len(doc[service]) == 0 {
		delete(doc, service)
	}
	return writeJSON(k.path, doc)
}

func (k *FileKeyring) load() (map[string]map[string]string, error) {
	k.checkPerms()
	doc := map[string]map[string]string{}
	if _, err := readJSON(k.path, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// checkPerms warns when the secret file is readable by anyone but its owner.
func (k *FileKeyring) checkPerms() {
	if k.warn == nil {
		return
	}
	info, err := os.Stat(k.path)
	if err != nil {
		return
	}
	if perm := info.Mode().Perm(); perm&^fs.FileMode(0o600) != 0 {
		fmt.Fprintf(
			k.warn,
			"wasa: %s has permissions %#o; expected 0600\n",
			k.path, perm,
		)
	}
}
