package transcript

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/nwverse/persona/server/internal/prompts"
	"github.com/nwverse/persona/server/internal/proto"
)

// UserInputPositions groups sorted items by input entry and extracts excerpts within each exchange.
func UserInputPositions(items []proto.Item) []proto.UserInputPosition {
	type entry struct {
		position      proto.UserInputPosition
		start         int
		present       bool
		hasBody       bool
		excluded      bool
		otherExchange bool
	}
	groups := map[string]int{}
	var order []entry
	for i, it := range items {
		if it.Role != "user" || (it.Kind != proto.KindText && it.Kind != proto.KindImage) || it.ToolUseID != "" {
			continue
		}
		id, _, _ := strings.Cut(it.ID, "#")
		at, found := groups[id]
		if !found {
			at = len(order)
			groups[id] = at
			order = append(order, entry{start: i, position: proto.UserInputPosition{ID: it.ID, Seq: it.Seq}})
		}
		g := &order[at]
		ignore, otherExchange := inputIndexOrigin(it)
		g.excluded = g.excluded || ignore
		g.otherExchange = g.otherExchange || otherExchange
		switch {
		case it.Kind == proto.KindImage:
			g.present = true
			g.position.Attachments++
		case !ignore:
			body, present, attachments := inputIndexBody(it.Text)
			if strings.HasPrefix(body, "/loop") {
				g.excluded = true
				g.otherExchange = g.otherExchange || !it.Queued
				continue
			}
			g.present = g.present || present
			g.hasBody = g.hasBody || body != ""
			if g.position.Preview == "" {
				g.position.Preview = inputIndexPreview(body)
			}
			g.position.Attachments += attachments
		}
	}
	out := make([]proto.UserInputPosition, 0, len(order))
	end := len(items)
	// Classify all blocks first. An image can precede the text that marks its entry as automated.
	for i := len(order) - 1; i >= 0; i-- {
		g := &order[i]
		if g.present && !g.excluded {
			g.position.Answer = inputIndexAnswer(items[g.start+1 : end])
			if g.hasBody {
				g.position.Attachments = 0
			}
			out = append(out, g.position)
			end = g.start
		} else if g.otherExchange {
			end = g.start
		}
	}
	slices.Reverse(out)
	return out
}

func inputIndexOrigin(it proto.Item) (ignore, otherExchange bool) {
	source, injected := prompts.ItemSource(it), it.Injected
	if injected == nil && (source == nil || source.Kind == proto.SourceFollowup) && it.ClientMsgID == "" &&
		it.Kind == proto.KindText && it.From == "" {
		injected = prompts.ParseInjected(it.Text)
	}
	ignore = it.From != "" || it.Wake || it.SupervisionUser || source != nil || injected != nil
	otherExchange = ignore && !it.Queued && !prompts.ContinuesOwnWork(injected) &&
		(source == nil || source.Kind != proto.SourceAutoContinue)
	return
}

// UserInputs reads the complete existing history, including a cold file's older windows. A partial scan never
// masquerades as the total. A positive before sequence restricts the projection to earlier source lines.
func (f *File) UserInputs(ctx context.Context, before int) (proto.UserInputIndex, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for f.base > 0 {
		if err := ctx.Err(); err != nil {
			return proto.UserInputIndex{}, err
		}
		if f.loadOlder() == 0 {
			return proto.UserInputIndex{}, errors.New("user input history is incomplete")
		}
	}
	if err := ctx.Err(); err != nil {
		return proto.UserInputIndex{}, err
	}
	positions := UserInputPositions(f.items[:f.before(before)])
	return proto.UserInputIndex{Total: len(positions), Inputs: positions, History: f.history()}, nil
}
