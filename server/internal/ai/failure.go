package ai

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/nwverse/persona/server/internal/lazyre"
)

// FailureKind is a direct-call failure whose remedy differs from an ordinary rejected request.
type FailureKind uint8

const (
	FailureOther FailureKind = iota
	// FailureModelUnavailable means the selected model does not exist or is not available to this key.
	FailureModelUnavailable
	// FailureContextTooLong means the input exceeds the model's context, not its output-token budget.
	FailureContextTooLong
)

// FailureKindOf reports a typed upstream failure. Pauses, refusals and unclassified errors are FailureOther.
func FailureKindOf(err error) FailureKind {
	var classified interface{ failureKind() FailureKind }
	if errors.As(err, &classified) {
		return classified.failureKind()
	}
	return FailureOther
}

// IsModelUnavailable tells a missing or key-inaccessible model from a missing endpoint or an invalid key.
func IsModelUnavailable(err error) bool { return FailureKindOf(err) == FailureModelUnavailable }

// IsContextTooLong tells an over-context input from an ordinary invalid request or a truncated reply.
func IsContextTooLong(err error) bool { return FailureKindOf(err) == FailureContextTooLong }

// FailureKindFor classifies a gateway's HTTP error body for callers such as connection probes. An explicit,
// recognized error.code wins over wording. An unknown code retains the wording-based behavior.
func FailureKindFor(status int, raw []byte) FailureKind {
	code, message := gatewayErrorDetails(raw)
	return failureKindFor(status, code, message)
}

// kindedError adds a classification without changing an existing error's text or unwrap chain.
type kindedError struct {
	error
	kind FailureKind
}

func (e *kindedError) Unwrap() error            { return e.error }
func (e *kindedError) failureKind() FailureKind { return e.kind }

func withFailureKind(err error, kind FailureKind) error {
	if err == nil || kind == FailureOther {
		return err
	}
	return &kindedError{error: err, kind: kind}
}

var (
	modelFailureWords   = lazyre.New(`(?i)model[^\n]{0,80}(not.{0,4}(found|exist)|does not exist|invalid|unknown|unsupported|unavailable)|model_not_found|invalid model|unknown model|knows no model`)
	contextFailureWords = lazyre.New(`(?i)prompt is too long|maximum (?:context|prompt) length|context.{0,20}(length|window).{0,40}(exceed|too)|(input|request|message).{0,20}exceeds?.{0,40}context.{0,20}(length|window)|context_length_exceeded`)
)

func failureKindFor(status int, code, message string) FailureKind {
	// Codes are machine semantics; a type such as not_found_error is deliberately not accepted here.
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "model_not_found":
		return FailureModelUnavailable
	case "context_length_exceeded":
		return FailureContextTooLong
	case "unsupported_endpoint":
		return FailureOther
	}
	if status != http.StatusBadRequest && status != http.StatusNotFound {
		return FailureOther
	}
	if modelFailureWords.MatchString(message) {
		return FailureModelUnavailable
	}
	if contextFailureWords.MatchString(message) {
		return FailureContextTooLong
	}
	return FailureOther
}

// gatewayErrorDetails reads OpenAI- and Anthropic-shaped envelopes. error.code is more specific than a top-level
// code; either may carry the message. Non-JSON bodies remain available to the established wording fallback.
func gatewayErrorDetails(raw []byte) (code, message string) {
	var body struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if json.Unmarshal(raw, &body) != nil {
		return "", string(raw)
	}
	code, message = strings.TrimSpace(body.Code), body.Message
	var errorText string
	if json.Unmarshal(body.Error, &errorText) == nil && errorText != "" {
		message = errorText
	}
	var nested struct {
		Code    string          `json:"code"`
		Message string          `json:"message"`
		Error   json.RawMessage `json:"error"`
	}
	if len(body.Error) > 0 && json.Unmarshal(body.Error, &nested) == nil {
		if c := strings.TrimSpace(nested.Code); c != "" {
			code = c
		}
		if nested.Message != "" {
			message = nested.Message
		}
		// Some envelopes put the actual error one level deeper, under error.error.
		if len(nested.Error) > 0 {
			var deeper struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if json.Unmarshal(nested.Error, &deeper) == nil {
				if c := strings.TrimSpace(deeper.Code); c != "" {
					code = c
				}
				if deeper.Message != "" {
					message = deeper.Message
				}
			}
		}
	}
	return code, message
}
