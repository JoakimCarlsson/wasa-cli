package record

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactKnownFormats(t *testing.T) {
	// New-provider tokens are assembled from prefix + body at runtime rather
	// than written as contiguous literals, so upstream secret scanners do not
	// flag these synthetic test fixtures. body is 36 alphanumerics, long
	// enough to satisfy every rule's length floor.
	body := "abcdefghijklmnopqrstuvwxyz0123456789"
	secrets := []string{
		"ghp_abcdefghijklmnopqrstuvwxyz0123456789",
		"github_pat_11ABCDEFG0123456789_abcdefghij",
		"glpat-abcdefghij0123456789",
		"sk-ant-api03-abcdefghijklmnopqrstuvwx",
		"sk-proj-abcdefghijklmnopqrstuvwx",
		"xoxb-1234567890-abcdefghij",
		"AKIAIOSFODNN7EXAMPLE",
		"AIzaSyA1234567890abcdefghijklmnopqrstuv",
		"eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxIn0.abcdef123456_-",
		// Stripe: secret key regression plus publishable/restricted.
		"sk_live_" + body,
		"sk_test_" + body,
		"pk_live_" + body,
		"rk_test_" + body,
		// Supabase, all three variants.
		"sb_secret_" + body,
		"sbp_" + body,
		"sbs_" + body,
		// npm access token (prefix + exactly 36 chars).
		"npm_" + body,
		// PyPI upload token.
		"pypi-" + body,
		// SendGrid API key (SG.<22+>.<22+>).
		"SG." + body + "." + body,
		// Twilio API key (SK + 32 hex).
		"SK" + strings.Repeat("0123456789abcdef", 2),
		// DigitalOcean, all three variants.
		"dop_v1_" + body,
		"doo_v1_" + body,
		"dor_v1_" + body,
		// Hugging Face user access token.
		"hf_" + body,
	}
	for _, s := range secrets {
		in := "before " + s + " after"
		out := string(Redact([]byte(in)))
		if strings.Contains(out, s) {
			t.Errorf("secret survived redaction: %q -> %q", s, out)
		}
		if !strings.Contains(out, placeholder) {
			t.Errorf("no placeholder for %q: %q", s, out)
		}
	}
}

func TestRedactKeepsContext(t *testing.T) {
	cases := map[string]string{
		"Authorization: Bearer abcdef1234567890abcdef": "Bearer " + placeholder,
		`api_key="supersecretvalue123"`:                "api_key=",
		`"password": "hunter2hunter2"`:                 `"password": "`,
	}
	for in, want := range cases {
		out := string(Redact([]byte(in)))
		if !strings.Contains(out, want) {
			t.Errorf("Redact(%q) = %q, want it to contain %q", in, out, want)
		}
		if out == in {
			t.Errorf("Redact(%q) left the secret in place", in)
		}
	}
}

func TestRedactPrivateKeyBlock(t *testing.T) {
	in := "x\n-----BEGIN RSA PRIVATE KEY-----\nMIIEow\nqqq\n" +
		"-----END RSA PRIVATE KEY-----\ny"
	out := string(Redact([]byte(in)))
	if strings.Contains(out, "MIIEow") {
		t.Errorf("private key material survived: %q", out)
	}
}

func TestRedactLeavesPlainTextAlone(t *testing.T) {
	in := "please rename the getToken function and fix the password " +
		"validation tests"
	if out := string(Redact([]byte(in))); out != in {
		t.Errorf("benign text changed: %q -> %q", in, out)
	}
}

func TestRedactLeavesLookalikesAlone(t *testing.T) {
	// Prefixes match the new provider rules but fall short of the length
	// floors or token shape, so they must survive untouched.
	benign := []string{
		"run npm install some-package to continue",
		"sbp_short is not a token",
		"hf_tooshort should stay",
		"the value SKnothexcharactersheregoesonand is fine",
		"see SG. Then read the next paragraph carefully",
		"pypi-short",
		"dop_v1_short",
	}
	for _, in := range benign {
		if out := string(Redact([]byte(in))); out != in {
			t.Errorf("benign text changed: %q -> %q", in, out)
		}
	}
}

func TestRedactKeepsJSONValid(t *testing.T) {
	line := map[string]string{
		"text": "my key is ghp_abcdefghijklmnopqrstuvwxyz0123456789 ok",
	}
	raw, err := json.Marshal(line)
	if err != nil {
		t.Fatal(err)
	}
	out := Redact(raw)
	var back map[string]string
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("redacted JSON no longer parses: %v: %s", err, out)
	}
	if strings.Contains(back["text"], "ghp_") {
		t.Errorf("token survived inside JSON: %q", back["text"])
	}
}
