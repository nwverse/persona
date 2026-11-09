package proto

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"
	"sync"
)

// The same name table as integrations/runtime-sdk/lib/items.js secretKey: parts of a name that say it holds a
// credential wherever they are (apiKey, x-api-key, client_secret, accessKeyId), and whole words that say so only
// alone (key, pass, auth…, so author, oauth_scope, session_name and keyword are not). A DSN, a connection string or
// a webhook URL carries its credential inside the value, so those names count too. The patterns are compiled on first
// use, not whenever a program that links proto starts (a hook, #1308); a contract depends on the standard library only.
var (
	secretParts = lazy(`token|passw|secret|apikey|accesskey|privatekey|credential|cookie|bearer|jwt|signature|sessionid|sessionkey|header|totp|webhook|connectionstring|connstr`)
	secretWords = map[string]bool{"key": true, "keys": true, "pass": true, "pwd": true, "pin": true, "auth": true,
		"authorization": true, "authentication": true, "authn": true, "sig": true, "otp": true, "dsn": true}
	camelBreak = lazy(`([a-z0-9])([A-Z])`)
	// an acronym before a word (SSHKey, DBPass): two capitals or more, so OAuth stays one word
	acronymBreak = lazy(`([A-Z]{2,})([A-Z][a-z])`)
	nonWord      = lazy(`[^a-z0-9]+`)
)

// lazy is regexp.MustCompile(src) on first use.
func lazy(src string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(src) })
}

// Redacted is what a credential's value becomes in RedactArgs.
const Redacted = "***"

// SecretKey is whether an argument's name says its value is a credential, case and separators aside.
func SecretKey(k string) bool {
	var words []string
	for _, w := range nonWord().Split(strings.ToLower(camelBreak().ReplaceAllString(acronymBreak().ReplaceAllString(k, "${1}_${2}"), "${1}_${2}")), -1) {
		if w != "" {
			words = append(words, w)
		}
	}
	if secretParts().MatchString(strings.Join(words, "")) {
		return true
	}
	for _, w := range words {
		if secretWords[w] {
			return true
		}
	}
	return false
}

// ArgGist is a tool call's first top-level string argument, in the order given, that is not blank and not named
// like a credential, as "name: value"; "" when there is none or input is not an object.
func ArgGist(input json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(input))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return ""
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return ""
		}
		k, _ := t.(string)
		var v json.RawMessage
		if dec.Decode(&v) != nil {
			return ""
		}
		var s string
		if SecretKey(k) || json.Unmarshal(v, &s) != nil || strings.TrimSpace(s) == "" {
			continue
		}
		return k + ": " + s
	}
	return ""
}

// RedactArgs is input as compact JSON with the value of every key named like a credential, at any depth and
// inside arrays, replaced by Redacted; "" when input is not JSON.
func RedactArgs(input json.RawMessage) string {
	dec := json.NewDecoder(bytes.NewReader(input))
	dec.UseNumber()
	var v any
	if dec.Decode(&v) != nil {
		return ""
	}
	b, err := json.Marshal(redact(v))
	if err != nil {
		return ""
	}
	return string(b)
}

func redact(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, x := range t {
			if SecretKey(k) {
				t[k] = Redacted
			} else {
				t[k] = redact(x)
			}
		}
	case []any:
		for i, x := range t {
			t[i] = redact(x)
		}
	}
	return v
}
