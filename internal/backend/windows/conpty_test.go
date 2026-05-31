//go:build windows

package conpty

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestConPtyProducesOutput(t *testing.T) {
	if !supported() {
		t.Skip("pseudo-console API not available")
	}
	cp, err := startConPty(120, 30, "", nil, []string{"cmd"})
	if err != nil {
		t.Fatalf("startConPty: %v", err)
	}

	var mu sync.Mutex
	var buf []byte
	var readErr error
	go func() {
		b := make([]byte, 4096)
		for {
			n, e := cp.out.Read(b)
			mu.Lock()
			buf = append(buf, b[:n]...)
			if e != nil {
				readErr = e
			}
			mu.Unlock()
			if e != nil {
				return
			}
		}
	}()

	time.Sleep(1 * time.Second)
	_, _ = cp.in.Write([]byte("echo conpty-works\r\n"))
	time.Sleep(2 * time.Second)
	exited := cp.Exited()
	mu.Lock()
	got := string(buf)
	gotErr := readErr
	mu.Unlock()
	cp.Close()

	t.Logf(
		"read %d bytes, child exited=%v, readErr=%v",
		len(got),
		exited,
		gotErr,
	)
	if !strings.Contains(got, "conpty-works") {
		t.Fatalf("output did not contain sentinel: %q", got)
	}
}

func TestConPtyInjectsEnv(t *testing.T) {
	if !supported() {
		t.Skip("pseudo-console API not available")
	}
	cp, err := startConPty(
		120, 30, "",
		[]string{"WASA_TEST_VAR=injected-value"},
		[]string{"cmd", "/k", "echo VAR=[%WASA_TEST_VAR%]"},
	)
	if err != nil {
		t.Fatalf("startConPty: %v", err)
	}

	var mu sync.Mutex
	var buf []byte
	go func() {
		b := make([]byte, 4096)
		for {
			n, e := cp.out.Read(b)
			mu.Lock()
			buf = append(buf, b[:n]...)
			mu.Unlock()
			if e != nil {
				return
			}
		}
	}()
	time.Sleep(2 * time.Second)
	mu.Lock()
	got := string(buf)
	mu.Unlock()
	cp.Close()

	if !strings.Contains(got, "VAR=[injected-value]") {
		t.Fatalf("injected env var not visible in output: %q", got)
	}
}
