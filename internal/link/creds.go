package link

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const credsFile = "credentials.json"

// Credentials is the stored link to a control plane. Token is a secret: the
// file is written 0600 and the token is never logged or printed.
type Credentials struct {
	URL   string `json:"url"`
	Token string `json:"token"`
}

// CredentialsPath returns the credential file location under $WASA_HOME.
func CredentialsPath(home string) string {
	return filepath.Join(home, credsFile)
}

// LoadCredentials reads the stored credentials. A missing file returns
// ok=false and no error — the runner simply is not linked.
func LoadCredentials(home string) (Credentials, bool, error) {
	data, err := os.ReadFile(CredentialsPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return Credentials{}, false, nil
	}
	if err != nil {
		return Credentials{}, false, err
	}
	var c Credentials
	if err := json.Unmarshal(data, &c); err != nil {
		return Credentials{}, false, fmt.Errorf(
			"credentials %s: %w", CredentialsPath(home), err,
		)
	}
	if c.URL == "" || c.Token == "" {
		return Credentials{}, false, fmt.Errorf(
			"credentials %s: missing url or token", CredentialsPath(home),
		)
	}
	return c, true, nil
}

// SaveCredentials writes the credentials with owner-only permissions, via a
// temp file and rename so a partial write never corrupts the stored token.
func SaveCredentials(home string, c Credentials) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(home, credsFile+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, CredentialsPath(home))
}

// DeleteCredentials removes the stored credentials. A missing file is fine.
func DeleteCredentials(home string) error {
	err := os.Remove(CredentialsPath(home))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}
