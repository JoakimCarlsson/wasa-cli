package link

import (
	"os"
	"runtime"
	"testing"
)

func TestCredentialsRoundTrip(t *testing.T) {
	home := t.TempDir()

	if _, ok, err := LoadCredentials(home); err != nil || ok {
		t.Fatalf("missing file: ok=%v err=%v, want unlinked", ok, err)
	}

	want := Credentials{URL: "http://localhost:8472", Token: "secret"}
	if err := SaveCredentials(home, want); err != nil {
		t.Fatal(err)
	}

	got, ok, err := LoadCredentials(home)
	if err != nil || !ok {
		t.Fatalf("load: ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Fatalf("load = %+v, want %+v", got, want)
	}

	if runtime.GOOS != "windows" {
		info, err := os.Stat(CredentialsPath(home))
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("credentials mode = %o, want 600", perm)
		}
	}

	if err := DeleteCredentials(home); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := LoadCredentials(home); ok {
		t.Fatal("credentials still load after delete")
	}
	if err := DeleteCredentials(home); err != nil {
		t.Fatalf("second delete: %v", err)
	}
}
