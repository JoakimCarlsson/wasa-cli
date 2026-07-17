package record

import (
	"bytes"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// TestRecorderConformance holds every supported recorder to the same
// normalized-event contract against its captured session fixture: valid roles,
// at least one user and one assistant turn, reconstructible raw bytes, the
// declared timestamp behavior, the expected intent, and — for line-based
// formats — a byte-exact denormalize round-trip. Corrupting a session fixture
// (dropping a role, renaming a type) fails this test, which is how a silent
// upstream format change is caught.
func TestRecorderConformance(t *testing.T) {
	for _, spec := range recorderSpecs {
		t.Run(spec.tool, func(t *testing.T) {
			native := readFixture(t, spec.tool, "session."+spec.ext)
			msgs := spec.r.Normalize(native)
			if len(msgs) == 0 {
				t.Fatal("session fixture produced no messages")
			}

			var users, assistants int
			for i, m := range msgs {
				switch m.Role {
				case "user":
					users++
				case "assistant":
					assistants++
				case "":
				default:
					t.Errorf("msg %d has unexpected role %q", i, m.Role)
				}
				if len(m.Raw) == 0 {
					t.Errorf("msg %d has empty Raw (not reconstructible)", i)
				}
				turn := m.Role == "user" || m.Role == "assistant"
				if turn && !m.Timestamp.IsZero() != spec.hasTimestamps {
					t.Errorf(
						"msg %d timestamp present=%v, want %v",
						i, !m.Timestamp.IsZero(), spec.hasTimestamps,
					)
				}
			}
			if users == 0 || assistants == 0 {
				t.Errorf(
					"want >=1 user and >=1 assistant turn, got users=%d "+
						"assistants=%d", users, assistants,
				)
			}

			if got := spec.r.Intent(native); got != spec.wantIntent {
				t.Errorf("Intent = %q, want %q", got, spec.wantIntent)
			}

			if spec.lineBased {
				assertRoundTrip(t, spec.tool, native)
			}
		})
	}
}

// assertRoundTrip checks that a line-based transcript survives
// normalize→denormalize byte-for-byte, so a checkpoint restore hands the agent
// back its own native format for a native resume.
func assertRoundTrip(t *testing.T, tool string, native []byte) {
	t.Helper()
	stored := normalize(tool, native)
	if !looksNormalized(stored) {
		t.Fatal("line-based fixture not detected as normalized")
	}
	if got := denormalize(stored); !bytes.Equal(got, native) {
		t.Errorf("round-trip mismatch:\n got: %q\nwant: %q", got, native)
	}
}

// TestRecorderEmptyAndMalformed checks the degenerate inputs every recorder
// must tolerate: an empty transcript yields no user/assistant turns and no
// intent, and a malformed transcript (truncated/garbage lines) parses without
// panicking and never invents a role outside the contract.
func TestRecorderEmptyAndMalformed(t *testing.T) {
	for _, spec := range recorderSpecs {
		t.Run(spec.tool, func(t *testing.T) {
			empty := readFixture(t, spec.tool, "empty."+spec.ext)
			for _, m := range spec.r.Normalize(empty) {
				if m.Role == "user" || m.Role == "assistant" {
					t.Errorf(
						"empty fixture produced a %q turn: %q",
						m.Role,
						m.Content,
					)
				}
			}
			if got := spec.r.Intent(empty); got != "" {
				t.Errorf("empty fixture Intent = %q, want \"\"", got)
			}

			malformed := readFixture(t, spec.tool, "malformed."+spec.ext)
			for i, m := range spec.r.Normalize(malformed) {
				if m.Role != "" && m.Role != "user" && m.Role != "assistant" {
					t.Errorf(
						"malformed msg %d has unexpected role %q",
						i,
						m.Role,
					)
				}
			}
			spec.r.Intent(malformed)
		})
	}
}

// TestRecorderSpecsCoverRegistry keeps recorderSpecs in lockstep with the
// recorders slice: a recorder added without a conformance row (or a stale row)
// fails here, so the fixture contract can never silently skip an agent.
func TestRecorderSpecsCoverRegistry(t *testing.T) {
	if len(recorderSpecs) != len(recorders) {
		t.Errorf(
			"recorderSpecs has %d rows, recorders has %d",
			len(recorderSpecs), len(recorders),
		)
	}
	for _, r := range recorders {
		specFor(t, r.Tool())
	}
}

// TestFixturesRedacted asserts Redact is a no-op over every fixture: the
// committed fixtures carry no detectable secret, exercising the same redaction
// path checkpoints are written through (checkpoint.go).
func TestFixturesRedacted(t *testing.T) {
	root := "testdata"
	err := filepath.WalkDir(
		root,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || strings.EqualFold(d.Name(), "README.md") {
				return nil
			}
			b := readFixture(t, filepath.Base(filepath.Dir(path)), d.Name())
			if got := Redact(b); !bytes.Equal(got, b) {
				t.Errorf(
					"%s changed by Redact — it carries a detectable secret",
					path,
				)
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("walk testdata: %v", err)
	}
}
