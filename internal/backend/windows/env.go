//go:build windows

package conpty

import (
	"sort"
	"strings"

	"golang.org/x/sys/windows"
)

// mergeEnv overlays extra (KEY=VALUE entries) onto base, replacing matching keys
// case-insensitively as Windows environment variables are. Entries in extra
// without a '=' are ignored. The result is unordered; newEnvBlock sorts it.
func mergeEnv(base, extra []string) []string {
	byKey := make(map[string]string, len(base)+len(extra))
	order := make([]string, 0, len(base)+len(extra))
	put := func(entry string) {
		eq := strings.IndexByte(entry, '=')
		if eq <= 0 {
			return
		}
		key := strings.ToUpper(entry[:eq])
		if _, seen := byKey[key]; !seen {
			order = append(order, key)
		}
		byKey[key] = entry
	}
	for _, e := range base {
		put(e)
	}
	for _, e := range extra {
		put(e)
	}

	out := make([]string, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	return out
}

// newEnvBlock encodes env as a UTF-16, double-null-terminated environment block
// for CreateProcess with CREATE_UNICODE_ENVIRONMENT. The block is sorted
// case-insensitively, the order the Windows loader expects. The returned pointer
// addresses the first element of a backing array kept alive by that pointer, so
// it stays valid for the duration of the CreateProcess call that uses it.
func newEnvBlock(env []string) (*uint16, error) {
	sorted := append([]string(nil), env...)
	sort.Slice(sorted, func(i, j int) bool {
		return strings.ToUpper(sorted[i]) < strings.ToUpper(sorted[j])
	})

	var block []uint16
	for _, e := range sorted {
		u16, err := windows.UTF16FromString(e)
		if err != nil {
			return nil, err
		}
		block = append(block, u16...)
	}
	block = append(block, 0)

	return &block[0], nil
}
