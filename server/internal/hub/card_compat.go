package hub

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// featureCardRing: the client draws a ```persona-card of type ring (#3068). A client declares it in client_hello's
// features, or on a REST request in the X-Persona-Features header (comma-separated); a REST request without the
// header also counts when it matches a connection of the same device, platform and version that declared it.
//
// Every other client gets each ring rewritten on its way out (cardCompatItems): a ring the apps can draw becomes a
// stacked_bar of the same parts (what the total leaves over them a "remaining" rest), which every app has drawn since
// before #3068; one they cannot becomes Markdown (the title in bold, a "label: value unit" line per part, the note).
// Nothing is stored: the transcript keeps the ring, and a client that declares the feature later gets it whole.
const featureCardRing = "persona_card_ring"

// featuresHeader is how a REST request declares what it can draw (the same names as client_hello's features).
const featuresHeader = "X-Persona-Features"

// requestWants reports whether a REST request's client declared feature: in its X-Persona-Features header, or on a
// connection of the same device, platform and version (requestHasElicitation's rule).
func (h *Hub) requestWants(r *http.Request, feature string) bool {
	for _, v := range r.Header.Values(featuresHeader) {
		for _, f := range strings.Split(v, ",") {
			if strings.TrimSpace(f) == feature {
				return true
			}
		}
	}
	who, rc := principalOf(r.Context()), requestClientOf(r.Context())
	if rc.Platform == "" {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.clients {
		c.mu.Lock()
		matches := c.device == who.ID && c.meta.Platform == rc.Platform && c.meta.Version == rc.Version
		c.mu.Unlock()
		if matches && c.has(feature) {
			return true
		}
	}
	return false
}

// cardCompatItems is items as a client gets them: unchanged when it draws rings, else a copy with each assistant
// text's ring cards rewritten (ringCardsDowngraded). The node's slice is shared, so it is never written to.
func cardCompatItems(l i18n.Lang, ring bool, items []proto.Item) []proto.Item {
	if ring {
		return items
	}
	var out []proto.Item
	for i := range items {
		it := items[i]
		if it.Kind != proto.KindText || it.Role != "assistant" {
			continue
		}
		text := ringCardsDowngraded(l, it.Text)
		if text == it.Text {
			continue
		}
		if out == nil {
			out = append([]proto.Item(nil), items...)
		}
		out[i].Text = text
	}
	if out == nil {
		return items
	}
	return out
}

// ringCardsDowngraded is md with each complete ```persona-card of type ring written for a client that does not draw
// rings. Other blocks, and rings inside another code block (examples), stay as they are.
func ringCardsDowngraded(l i18n.Lang, md string) string {
	if !strings.Contains(md, "persona-card") || !strings.Contains(md, "ring") {
		return md
	}
	if !i18n.Valid(l) {
		l = i18n.ZhHans
	}
	var b strings.Builder
	at := 0
	for _, blk := range wire.AssistantViewBlocks(md) {
		if blk.Language != "persona-card" || !blk.Complete {
			continue
		}
		var c wire.PersonaCard
		if len(blk.Raw) > wire.AssistantViewMost || json.Unmarshal(blk.Raw, &c) != nil || c.Type != wire.PersonaCardKindRing {
			continue
		}
		b.WriteString(md[at:blk.Start])
		b.WriteString(ringReplacement(l, c))
		if strings.HasSuffix(md[blk.Start:blk.End], "\n") {
			b.WriteString("\n")
		}
		at = blk.End
	}
	if at == 0 {
		return md
	}
	b.WriteString(md[at:])
	return b.String()
}

// ringReplacement is a ring as an older client draws it: a stacked_bar block, or Markdown when the apps could not draw
// the ring either (no trailing line break).
func ringReplacement(l i18n.Lang, c wire.PersonaCard) string {
	parts := append([]wire.CardPart{}, c.Segments...)
	if c.Rest != nil {
		parts = append(parts, *c.Rest)
	}
	sum := 0.0
	for _, p := range parts {
		sum += p.Value
	}
	remaining := tt(l, "card_remaining")
	if c.View() != nil {
		bar := wire.PersonaCard{Type: wire.PersonaCardKindStackedBar, Title: c.Title, Subtitle: c.Subtitle, Unit: c.Unit,
			Note: c.Note, Total: c.Total, Segments: parts}
		if c.Total != nil && c.Total.Value > sum {
			bar.Rest = &wire.CardPart{Label: remaining, Value: c.Total.Value - sum}
		}
		if raw, err := json.Marshal(bar); err == nil {
			return "```persona-card\n" + string(raw) + "\n```"
		}
	}
	unit := c.Unit
	if c.Total != nil && c.Total.Unit != "" {
		unit = c.Total.Unit
	}
	amount := func(v float64) string {
		if unit == "" {
			return wire.Number(v)
		}
		return wire.Number(v) + " " + unit
	}
	var lines []string
	if c.Title != "" {
		lines = append(lines, "**"+c.Title+"**")
	}
	if c.Subtitle != "" {
		lines = append(lines, c.Subtitle)
	}
	if c.Total != nil {
		lines = append(lines, "- "+tt(l, "card_total")+": "+amount(c.Total.Value))
	}
	for _, p := range parts {
		line := "- " + p.Label + ": " + amount(p.Value)
		if p.Note != "" {
			line += " (" + p.Note + ")"
		}
		lines = append(lines, line)
	}
	if c.Total != nil && c.Total.Value > sum {
		lines = append(lines, "- "+remaining+": "+amount(c.Total.Value-sum))
	}
	if c.Note != "" {
		lines = append(lines, "", c.Note)
	}
	return strings.Join(lines, "\n")
}
