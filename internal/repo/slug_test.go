package repo

import "testing"

func TestSlug(t *testing.T) {
	tests := []struct {
		remote string
		want   string
	}{
		{"https://github.com/acme/widgets.git", "acme/widgets"},
		{"https://github.com/acme/widgets", "acme/widgets"},
		{"git@github.com:acme/widgets.git", "acme/widgets"},
		{"ssh://git@github.com/acme/widgets.git", "acme/widgets"},
		{"git://example.com/acme/widgets", "acme/widgets"},
		{"https://gitlab.com/acme/team/widgets.git", "team/widgets"},
		{"  https://github.com/acme/widgets.git  ", "acme/widgets"},
		{"", ""},
		{"/srv/git/widgets", ""},
		{"../sibling", ""},
		{"widgets", ""},
		{"https://github.com/widgets", ""},
	}
	for _, tt := range tests {
		if got := Slug(tt.remote); got != tt.want {
			t.Errorf("Slug(%q) = %q, want %q", tt.remote, got, tt.want)
		}
	}
}
