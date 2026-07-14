package record

import "regexp"

// placeholder replaces every detected secret in a redacted transcript. It
// contains no quote or backslash so substituting it inside a JSON string
// keeps the line valid JSON.
const placeholder = "[REDACTED]"

// redaction is one supported secret format: a pattern and its replacement
// template. repl defaults to the bare placeholder; a pattern that must keep
// surrounding context (a key name, the word Bearer) captures it and re-emits
// it around the placeholder.
type redaction struct {
	re   *regexp.Regexp
	repl string
}

// redactions are the supported secret formats. Detection is best-effort by
// design: it catches the common token shapes, not every possible credential.
var redactions = []redaction{
	// GitHub tokens, classic and fine-grained.
	{re: regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`)},
	{re: regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{20,}`)},
	// GitLab personal access tokens.
	{re: regexp.MustCompile(`\bglpat-[A-Za-z0-9_-]{20,}`)},
	// OpenAI / Anthropic style secret keys.
	{re: regexp.MustCompile(`\bsk-[A-Za-z0-9_-]{20,}`)},
	// Stripe live/test keys (publishable, restricted, secret).
	{re: regexp.MustCompile(`\b[prs]k_(?:live|test)_[A-Za-z0-9]{20,}`)},
	// Supabase secret/service/personal tokens.
	{re: regexp.MustCompile(`\bsb(?:_secret|p|s)_[A-Za-z0-9]{20,}`)},
	// npm access tokens.
	{re: regexp.MustCompile(`\bnpm_[A-Za-z0-9]{36}`)},
	// PyPI upload tokens.
	{re: regexp.MustCompile(`\bpypi-[A-Za-z0-9_-]{20,}`)},
	// SendGrid API keys.
	{re: regexp.MustCompile(`\bSG\.[A-Za-z0-9_-]{22,}\.[A-Za-z0-9_-]{22,}`)},
	// Twilio API keys.
	{re: regexp.MustCompile(`\bSK[0-9a-f]{32}\b`)},
	// DigitalOcean tokens (personal, OAuth, refresh).
	{re: regexp.MustCompile(`\bdo[opr]_v1_[A-Za-z0-9]{20,}`)},
	// Hugging Face user access tokens.
	{re: regexp.MustCompile(`\bhf_[A-Za-z0-9]{30,}`)},
	// Slack tokens.
	{re: regexp.MustCompile(`\bxox[baprs]-[A-Za-z0-9-]{10,}`)},
	// AWS access key ids.
	{re: regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	// Google API keys.
	{re: regexp.MustCompile(`\bAIza[0-9A-Za-z_-]{35}`)},
	// JWTs.
	{re: regexp.MustCompile(
		`\beyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}`,
	)},
	// Private key blocks (also PEM blocks split across a JSON string).
	{re: regexp.MustCompile(
		`-----BEGIN [A-Z ]*PRIVATE KEY-----` +
			`(?s).*?` +
			`(?:-----END [A-Z ]*PRIVATE KEY-----|\z)`,
	)},
	// Bearer credentials.
	{
		re:   regexp.MustCompile(`(?i)\b(bearer\s+)[A-Za-z0-9._~+/-]{16,}`),
		repl: "${1}" + placeholder,
	},
	// Generic key/value assignments: api_key=..., "token": "...", etc.
	{
		re: regexp.MustCompile(
			`(?i)\b(api[_-]?key|access[_-]?token|auth[_-]?token|` +
				`client[_-]?secret|secret[_-]?key|password|passwd)` +
				`(\\?["']?\s*[:=]\s*\\?["']?)[^\s"'\\,;&]{8,}`,
		),
		repl: "${1}${2}" + placeholder,
	},
}

// Redact replaces every detected secret in b with a placeholder. It is
// best-effort: the supported patterns cover common API key, token and
// credential formats, not every possible secret.
func Redact(b []byte) []byte {
	for _, r := range redactions {
		repl := r.repl
		if repl == "" {
			repl = placeholder
		}
		b = r.re.ReplaceAll(b, []byte(repl))
	}
	return b
}
