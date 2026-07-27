//go:build !windows

package gitremote

import "testing"

const testCore = "https://core.example/api"

func TestResolveSlugRemote(t *testing.T) {
	got, err := Resolve(testCore, "wasa://acme/widgets")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Audience != "/et/acme/widgets" {
		t.Errorf("audience = %q", got.Audience)
	}
	if got.URL != testCore+"/et/acme/widgets" {
		t.Errorf("url = %q", got.URL)
	}
}

func TestResolveRepoIDRemote(t *testing.T) {
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	got, err := Resolve(testCore, "wasa://"+id)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Audience != "/git/repo/"+id {
		t.Errorf("audience = %q", got.Audience)
	}
	if got.URL != testCore+"/git/repo/"+id {
		t.Errorf("url = %q", got.URL)
	}
}

func TestResolveAcceptsGitSuffixAndTransportForm(t *testing.T) {
	for _, raw := range []string{
		"wasa://acme/widgets.git",
		"wasa:acme/widgets",
		"acme/widgets",
		"wasa://acme/widgets/",
	} {
		got, err := Resolve(testCore, raw)
		if err != nil {
			t.Fatalf("Resolve(%q): %v", raw, err)
		}
		if got.Audience != "/et/acme/widgets" {
			t.Errorf("Resolve(%q) audience = %q", raw, got.Audience)
		}
	}
}

func TestResolveRejectsUnusableRemotes(t *testing.T) {
	for _, raw := range []string{
		"wasa://acme/widgets/extra",
		"wasa://acme",
		"wasa://acme/..",
		"wasa://../etc",
		"wasa://acme/wid gets",
		"wasa://acme/widgets?service=git-receive-pack",
		"wasa://",
		"",
	} {
		if got, err := Resolve(testCore, raw); err == nil {
			t.Errorf("Resolve(%q) = %+v, want an error", raw, got)
		}
	}
}

func TestResolveRejectsAnUnusableCore(t *testing.T) {
	for _, coreURL := range []string{"", "ftp://core.example", "not a url"} {
		if _, err := Resolve(coreURL, "wasa://acme/widgets"); err == nil {
			t.Errorf("Resolve with core %q: want an error", coreURL)
		}
	}
}
