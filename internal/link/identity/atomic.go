//go:build !windows

package identity

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// readJSON decodes path into v. A missing file leaves v untouched and reports
// false so a fresh user is not an error. A corrupt file self-heals: it is
// reported as missing rather than wedging every later command.
func readJSON(path string, v any) (bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, nil
	}
	return true, nil
}

// writeJSON encodes v to path at mode 0600 via a temp file in the same
// directory renamed into place, so a partial write never corrupts the state
// that is already there.
func writeJSON(path string, v any) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
