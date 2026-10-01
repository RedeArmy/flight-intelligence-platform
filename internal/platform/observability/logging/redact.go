package logging

import (
	"log/slog"
	"regexp"
	"strings"

	"github.com/RedeArmy/flight-intelligence-platform/internal/shared/secret"
)

// sensitiveKeyTerms mark an attribute as sensitive when they occur in its normalised key. The match is
// deliberately broad (SR-09): an over-redacted key is a nuisance, a leaked credential is an incident.
var sensitiveKeyTerms = []string{
	"authorization", "api_key", "apikey", "password", "passwd", "passphrase", "token",
	"secret", "cookie", "credential", "private_key", "pepper",
}

var (
	// apiKeyPattern matches the platform API key format fip_<prefix>_<secret> (ADR-016).
	apiKeyPattern = regexp.MustCompile(`fip_[A-Za-z0-9]{6,16}_[A-Za-z0-9_-]{16,}`)
	// authSchemePattern matches "Bearer <token>" and "Basic <credentials>" in free text.
	authSchemePattern = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[A-Za-z0-9._~+/=-]{8,}`)
	// urlCredentialPattern matches the password part of scheme://user:password@host.
	urlCredentialPattern = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://[^:/\s@]+:)[^@\s]+@`)
	// keyValuePattern matches key=value and key: value forms such as password=abc or token: abc.
	keyValuePattern = regexp.MustCompile(`(?i)\b(password|passwd|pwd|secret|token|api[_-]?key)(["']?\s*[=:]\s*["']?)[^\s"',;&]+`)
)

// Scrub removes recognisable credentials from free text: platform API keys, bearer/basic tokens, URL passwords
// and key=value pairs such as password=... or token: .... It cannot find a secret written as plain prose with no
// recognisable shape, so the rule stays: never put a secret in a log message (use a secret.Secret attribute).
func Scrub(s string) string {
	s = apiKeyPattern.ReplaceAllString(s, secret.Redacted())
	s = authSchemePattern.ReplaceAllString(s, "$1 "+secret.Redacted())
	s = urlCredentialPattern.ReplaceAllString(s, "${1}"+secret.Redacted()+"@")
	return keyValuePattern.ReplaceAllString(s, "${1}${2}"+secret.Redacted())
}

func isSensitiveKey(key string) bool {
	k := strings.ToLower(key)
	k = strings.NewReplacer("-", "_", " ", "_", ".", "_").Replace(k)
	for _, term := range sensitiveKeyTerms {
		if strings.Contains(k, term) {
			return true
		}
	}
	return false
}

// redactAttr is the slog ReplaceAttr hook: it redacts sensitive keys and scrubs string and error values.
// It also runs on the built-in msg attribute, so a secret interpolated into a message is scrubbed too.
func redactAttr(_ []string, a slog.Attr) slog.Attr {
	if isSensitiveKey(a.Key) {
		return slog.String(a.Key, secret.Redacted())
	}
	return slog.Attr{Key: a.Key, Value: scrubValue(a.Value)}
}

func scrubValue(v slog.Value) slog.Value {
	v = v.Resolve() // runs LogValuer, so secret.Secret becomes [REDACTED]
	switch v.Kind() {
	case slog.KindString:
		return slog.StringValue(Scrub(v.String()))
	case slog.KindAny:
		if err, ok := v.Any().(error); ok {
			return slog.StringValue(Scrub(err.Error()))
		}
	}
	return v
}
