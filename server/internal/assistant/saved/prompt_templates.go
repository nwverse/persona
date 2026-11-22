package saved

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/i18n"
)

const (
	PromptTemplatesKey    = "assistant.saved_prompt_templates"
	promptTemplateVersion = 1

	templateFieldNameMax = 32
	templateFieldsMax    = 8
	templateSlotsMax     = 64
)

var (
	errPromptTemplateInvalid     = i18n.Err("assistant-saved-prompts.err_template_invalid")
	errPromptTemplateUnavailable = i18n.Err("assistant-saved-prompts.err_template_unavailable")
)

// A template flag follows the exact source text that the owner opted in.
// Older managers can rewrite the plain list without removing these flags and
// cannot enable templates for edited text.
type promptTemplateFlags map[string]string

func promptTextHash(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

func (flags promptTemplateFlags) enabled(p Prompt) bool {
	hash, ok := flags[p.ID]
	return ok && hash == promptTextHash(p.Text)
}

// apply updates only the marker for this edited prompt. An older client's PUT
// can keep the existing template on when its new source still has valid fields.
func (flags promptTemplateFlags) apply(p *Prompt, enabledBefore bool, useTemplate *bool) error {
	p.Template = false
	enable := enabledBefore
	if useTemplate != nil {
		enable = *useTemplate
	}
	if !enable {
		delete(flags, p.ID)
		return nil
	}
	if flags == nil {
		return errPromptTemplateUnavailable
	}
	if !validPromptTemplate(p.Text) {
		if useTemplate != nil {
			return errPromptTemplateInvalid
		}
		delete(flags, p.ID) // accept a legacy text edit as a plain prompt
		return nil
	}
	p.Template = true
	flags[p.ID] = promptTextHash(p.Text)
	return nil
}

// validPromptTemplate checks the one-pass template syntax. Escapes are only
// meaningful in text, outside a declaration. The actual values are typed and
// expanded in the client when the owner fills the composer.
func validPromptTemplate(text string) bool {
	fields := map[string]struct{}{}
	slots := 0
	for len(text) > 0 {
		switch {
		case strings.HasPrefix(text, `\{{`), strings.HasPrefix(text, `\}}`):
			text = text[3:]
		case strings.HasPrefix(text, `\\`):
			text = text[2:]
		case strings.HasPrefix(text, "}}"):
			return false
		case strings.HasPrefix(text, "{{"):
			name, tail, found := strings.Cut(text[2:], "}}")
			name = strings.Trim(name, " \t")
			if !found || !validTemplateField(name) {
				return false
			}
			fields[name] = struct{}{}
			slots++
			if len(fields) > templateFieldsMax || slots > templateSlotsMax {
				return false
			}
			text = tail
		default:
			_, width := utf8.DecodeRuneInString(text)
			text = text[width:]
		}
	}
	return slots > 0
}

func validTemplateField(name string) bool {
	if !utf8.ValidString(name) || utf8.RuneCountInString(name) > templateFieldNameMax {
		return false
	}
	first, width := utf8.DecodeRuneInString(name)
	if first != '_' && !unicode.IsLetter(first) {
		return false
	}
	for _, r := range name[width:] {
		if r != '_' && r != '-' && !unicode.IsLetter(r) && !unicode.IsDigit(r) && !unicode.IsMark(r) {
			return false
		}
	}
	return true
}
