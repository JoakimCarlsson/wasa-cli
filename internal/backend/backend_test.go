package backend

import "testing"

func TestDefaultReturnsBackend(t *testing.T) {
	if Default() == nil {
		t.Fatal("Default() returned nil; want a session backend")
	}
}
