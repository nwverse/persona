package transcript

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"golang.org/x/text/unicode/norm"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const ShareMessageLimit = 1 << 20
const ShareDisplayLimit = 32 << 10

var ErrShareComponentLimit = errors.New("sharing component exceeds complete-content limit")
var privateKeyStart = regexp.MustCompile(`(?i)-----BEGIN [A-Z0-9 ]*PRIVATE KEY-----`)
var privateKeyEnd = regexp.MustCompile(`(?i)-----END [A-Z0-9 ]*PRIVATE KEY-----`)
var shareBearer = regexp.MustCompile(`(?i)\bBearer[ \t]+[A-Za-z0-9._~+/=-]+`)
var shareAssignment = regexp.MustCompile(`(?i)\b(api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|passwd|authorization)\b(["']?[ \t]*[:=][ \t]*)("[^"\r\n]*"|'[^'\r\n]*'|[^\s,;}]+)`)
var shareCredential = regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9_]{16,}|github_pat_[A-Za-z0-9_]{16,}|AKIA[A-Z0-9]{16}|eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+)\b`)
var shareURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
var shareSensitiveKey = regexp.MustCompile(`(?i)^(?:api[_-]?key|access[_-]?token|refresh[_-]?token|secret|password|passwd|authorization|credential|private[_-]?key)$`)

// SanitizeShareText sees the entire decoded component before any display clipping.
// An unterminated private-key block removes everything through EOF. This is
// best-effort credential redaction, not a promise to remove sensitive prose.
func SanitizeShareText(s string) (string, error) {
	if len(s) > ShareMessageLimit || !utf8.ValidString(s) {
		return "", ErrShareComponentLimit
	}
	s = norm.NFKC.String(s)
	if len(s) > ShareMessageLimit {
		return "", ErrShareComponentLimit
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	for {
		begin := privateKeyStart.FindStringIndex(s)
		if begin == nil {
			break
		}
		end := privateKeyEnd.FindStringIndex(s[begin[1]:])
		stop := len(s)
		if end != nil {
			stop = begin[1] + end[1]
		}
		s = s[:begin[0]] + "[REDACTED PRIVATE KEY]" + s[stop:]
	}
	s = shareURL.ReplaceAllStringFunc(s, func(raw string) string {
		u, err := url.Parse(raw)
		if err != nil {
			return "[REDACTED URL]"
		}
		if u.User != nil {
			u.User = nil
		}
		q := u.Query()
		for k := range q {
			if shareSensitiveKey.MatchString(k) || strings.EqualFold(k, "token") {
				q.Set(k, "[REDACTED]")
			}
		}
		u.RawQuery = q.Encode()
		return u.String()
	})
	s = shareBearer.ReplaceAllString(s, "Bearer [REDACTED]")
	s = shareAssignment.ReplaceAllString(s, "${1}${2}[REDACTED]")
	s = shareCredential.ReplaceAllString(s, "[REDACTED]")
	return s, nil
}

// sanitizeShareSegments scans the entire logical text before choosing its final
// segment. If a private-key block spans segments it removes the separators too;
// omit that component rather than exposing an unmarked suffix as a final answer.
func sanitizeShareSegments(texts []string) ([]string, error) {
	separator := "\n" + rand.Text() + "\n"
	clean, err := SanitizeShareText(strings.Join(texts, separator))
	if err != nil {
		return nil, err
	}
	parts := strings.Split(clean, separator)
	if len(parts) != len(texts) {
		return nil, ErrShareComponentLimit
	}
	return parts, nil
}

func ShareDisplayText(s string) string {
	if len(s) <= ShareDisplayLimit {
		return s
	}
	end := ShareDisplayLimit - len("\n[Display truncated]")
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end] + "\n[Display truncated]"
}

// SanitizeShareArguments normalizes decoded strings and scans the serialized
// permitted fields as well. Sensitive object keys are retained, values removed.
func SanitizeShareArguments(raw json.RawMessage) (string, error) {
	if len(raw) > ShareMessageLimit {
		return "", ErrShareComponentLimit
	}
	var value any
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return "", err
	}
	var walk func(any) (any, error)
	walk = func(v any) (any, error) {
		switch x := v.(type) {
		case string:
			return SanitizeShareText(x)
		case []any:
			for i := range x {
				a, err := walk(x[i])
				if err != nil {
					return nil, err
				}
				x[i] = a
			}
			return x, nil
		case map[string]any:
			out := make(map[string]any, len(x))
			for k, a := range x {
				key, err := SanitizeShareText(k)
				if err != nil {
					return nil, err
				}
				if shareSensitiveKey.MatchString(k) {
					out[key] = "[REDACTED]"
					continue
				}
				a, err = walk(a)
				if err != nil {
					return nil, err
				}
				out[key] = a
			}
			return out, nil
		default:
			return v, nil
		}
	}
	normalized, err := walk(value)
	if err != nil {
		return "", err
	}
	b, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	return SanitizeShareText(string(b))
}
