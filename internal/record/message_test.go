package record

import "testing"

// TestSanitizeIntentStripsSeededContextPreamble guards the other half of the
// #190 fix. launch.wrapContext tags every seeded preamble as <context>; this
// asserts the stripper that makes that work is still present, since the two
// live in different packages and are coupled only by the literal tag. If this
// fails, intent.md has started recording injected history again and will nest a
// further copy every session.
func TestSanitizeIntentStripsSeededContextPreamble(t *testing.T) {
	seeded := "<context>\n" +
		"Recorded history from previous sessions in this repo (context only " +
		"— the user's request follows below):\n\n" +
		"── session abc · branch task/x · 2026-07-28 ──\n" +
		"intent:  something a previous session was asked\n" +
		"</context>\n\nimplement issue #7"

	if got := sanitizeIntent(seeded); got != "implement issue #7" {
		t.Errorf("sanitizeIntent = %q, want %q", got, "implement issue #7")
	}
}

// TestFirstUserIntentIgnoresSeededPreamble is the behaviour the recorder
// actually relies on: the preamble and the request arrive in one user message,
// so the extractor must return the request alone.
func TestFirstUserIntentIgnoresSeededPreamble(t *testing.T) {
	msgs := []Message{{
		Role: "user",
		Content: "<context>\nRecorded history from previous sessions in this " +
			"repo (context only):\n\nintent:  an older request\n</context>\n\n" +
			"add voting to the gallery",
	}}

	if got := firstUserIntent(msgs); got != "add voting to the gallery" {
		t.Errorf(
			"firstUserIntent = %q, want %q", got, "add voting to the gallery",
		)
	}
}
