package transcript

import (
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/compat"
	"github.com/nwverse/persona/server/internal/lazyre"
	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/textfmt"
)

const (
	userInputPreviewRunes = 100
	userInputAnswerRunes  = 180
)

var (
	indexMetadata          = lazyre.New(`(?s)<ADDITIONAL_METADATA\b[^>]*>.*?(?:</ADDITIONAL_METADATA>|$)`)
	indexFeedback          = lazyre.New(`(?s)<persona-view-errors\b[^>]*>.*?(?:</persona-view-errors>|$)`)
	indexReminder          = lazyre.New(`(?s)<system-reminder\b[^>]*>.*?(?:</system-reminder>|$)`)
	indexAttributedContext = lazyre.New(`<` + compat.ContextPattern() + `[[:space:]]+[^>]*>`)
	indexComment           = lazyre.New(`(?s)<!--.*?(?:-->|$)`)
	indexPhotoBlock        = lazyre.New(`(?s)<attached_images>.*?(?:</attached_images>|$)`)
	indexFile              = lazyre.New(`<` + compat.FilePattern() + `\b[^>]*(?:>|$)(?:\s*</` + compat.FilePattern() + `>)?`)
	indexFilePath          = lazyre.New(`\s+path="[^"]+"`)
	indexImageChip         = lazyre.New(`\[Image #\d+\]`)
	indexInlineImage       = lazyre.New(`(?i)!\[[^\]\r\n]*\](?:\([^\r\n)]*(?:\)|$)|\[[^\]\r\n]*\])|<img\b[^>]*(?:>|$)`)
	indexImageData         = lazyre.New(`(?i)\bdata:image/[^\s<>'")]+`)
)

func withoutIndexMetadata(s string) string {
	s = dropTagged(clippedNote, clipNotePrefix, s)
	s = dropTagged(indexMetadata, "<ADDITIONAL_METADATA", s)
	s = dropTagged(indexFeedback, "<persona-view-errors", s)
	return dropTagged(indexReminder, "<system-reminder", s)
}

// inputIndexBody separates the user's text from attachment references as in the chat bubble.
// An attributed context still occupies an input position even when there are no words to preview.
func inputIndexBody(raw string) (body string, present bool, attachments int) {
	raw = withoutIndexMetadata(dropTagged(indexComment, "<!--", raw))
	body = StripInjected(raw)
	if request, ok := strings.CutPrefix(body, "<USER_REQUEST>"); ok {
		body, _, _ = strings.Cut(request, "</USER_REQUEST>")
	}
	body = strings.TrimSpace(body)
	present = body != "" || (strings.Contains(raw, "-context") && indexAttributedContext.MatchString(raw))
	if strings.Contains(body, "<attached_images>") {
		photos := map[string]struct{}{}
		body = indexPhotoBlock.ReplaceAllStringFunc(body, func(block string) string {
			block = strings.TrimSuffix(strings.TrimPrefix(block, "<attached_images>"), "</attached_images>")
			for line := range strings.SplitSeq(block, "\n") {
				line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "path:"))
				// Antigravity can write a relative photo path in the same block.
				if filepath.IsAbs(line) || strings.HasPrefix(line, "~") || filepath.Ext(line) != "" {
					photos[filepath.Clean(line)] = struct{}{}
				}
			}
			return " "
		})
		attachments = len(photos)
	}
	if strings.Contains(body, "-file") {
		body = indexFile.ReplaceAllStringFunc(body, func(tag string) string {
			if indexFilePath.MatchString(tag) {
				attachments++
			}
			return " "
		})
	}
	body = dropTagged(indexImageChip, "[Image #", body)
	return strings.TrimSpace(withoutIndexImages(body)), present, attachments
}

func inputIndexPreview(body string) string {
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, ">") {
			continue
		}
		if textfmt.Cut(line, userInputPreviewRunes) != line {
			line = textfmt.Clip(line, userInputPreviewRunes-1)
		}
		return strings.Clone(line)
	}
	return ""
}

func inputIndexAnswer(items []proto.Item) string {
	var answer strings.Builder
	left := userInputAnswerRunes
	progress := ""
	for _, it := range items {
		if it.Wake {
			break // a wake opens a different exchange with no human input
		}
		if it.Role != "assistant" || it.Kind != proto.KindText || it.ToolUseID != "" || it.From != "" ||
			it.Injected != nil || it.Source != nil || it.SupervisionUser || it.CodexHeartbeatQuiet || it.IsError {
			continue
		}
		if it.Narration && answer.Len() > 0 {
			continue
		}
		prose := inputIndexProse(it.Text)
		if prose == "" || ReplyPlaceholder(prose) {
			continue
		}
		if it.Narration {
			progress = strings.TrimSpace(textfmt.Cut(prose, userInputAnswerRunes))
			continue
		}
		if answer.Len() > 0 {
			answer.WriteByte(' ')
			left--
		}
		head := strings.TrimSpace(textfmt.Cut(prose, left))
		answer.WriteString(head)
		left -= utf8.RuneCountInString(head)
		if head != prose || left < 2 {
			break
		}
	}
	if answer.Len() == 0 {
		return progress
	}
	return answer.String()
}

func inputIndexProse(raw string) string {
	raw = StripInjected(withoutIndexMetadata(raw))
	raw = dropTagged(indexPhotoBlock, "<attached_images>", raw)
	raw = dropTagged(indexFile, "-file", raw)
	prose := textfmt.MarkdownProse(raw)
	// A clipped inline comment can survive Markdown parsing as ordinary text.
	prose = dropTagged(indexComment, "<!--", prose)
	return textfmt.OneLine(withoutIndexImages(prose))
}

func withoutIndexImages(s string) string {
	if strings.Contains(s, "![") || strings.Contains(s, "<") {
		s = indexInlineImage.ReplaceAllString(s, " ")
	}
	if strings.Contains(s, ":") {
		s = indexImageData.ReplaceAllString(s, " ")
	}
	return s
}
