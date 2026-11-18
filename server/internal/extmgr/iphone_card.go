package extmgr

// The approval card of a step on an iPhone (docs/extensions/iphone.md "Approval"): the node core says what waits, as
// data (proto.IPhoneApproval); the words are the manager's, in the user's language, from the catalog
// (hub-iphone.card_*). The card's input is that data without the evidence (English facts for the log): never the
// model's own arguments, which can hold a password being typed.

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

// iphoneCardText is the most of what a step types that the card shows.
const iphoneCardText = 200

// iphoneCard is the card for approval a on iPhone phone, in l: its text, and its input.
func iphoneCard(l i18n.Lang, phone string, a *proto.IPhoneApproval) (string, json.RawMessage) {
	t := func(key string, args ...i18n.Args) string { return i18n.T(l, "hub-iphone.card_"+key, args...) }
	act := iphoneCardAction(l, a.Action, a.Target, a.At, a.Text, a.TextLen, true, a.Key, a.Button, a.LaunchApp)
	var lines []string
	if app := cardApp(a.App); app != "" {
		lines = append(lines, t("on_in", i18n.Args{"phone": phone, "app": app, "action": act}))
	} else {
		lines = append(lines, t("on", i18n.Args{"phone": phone, "action": act}))
	}
	var why []string
	for _, n := range a.Needs {
		switch n {
		case "payment", "delete", "send_message", "password", "confirm", "unchecked":
			why = append(why, t("need_"+n))
		}
	}
	if len(why) > 0 {
		lines = append(lines, t("why", i18n.Args{"reasons": strings.Join(why, t("sep"))}))
	}
	// With "ask before every action" one answer covers the steps after it too (a step the rules mark is still asked
	// on its own).
	var rest []string
	for i, s := range a.Steps {
		if i == 0 && s.Action == a.Action {
			continue // the step this card is about
		}
		rest = append(rest, iphoneCardAction(l, s.Action, s.Target, s.At, "", s.TextLen, false, s.Key, s.Button, s.App))
	}
	if len(rest) > 0 {
		lines = append(lines, t("then", i18n.Args{"steps": strings.Join(rest, t("arrow"))}))
	}
	shown := *a
	shown.Evidence = nil
	input, _ := json.Marshal(shown)
	return strings.Join(lines, "\n"), input
}

// iphoneCardAction is one step in words: "Tap “Pay”", "Type “hello” into item 4", "Tap at (120, 640)". withText: the
// step's text comes with it unless it is a password (the approval itself); a step summary only ever has its length.
func iphoneCardAction(l i18n.Lang, action string, target *proto.IPhoneElementRef, at *proto.IPhonePoint, text string, textLen int,
	withText bool, key, button, app string) string {
	t := func(key string, args ...i18n.Args) string { return i18n.T(l, "hub-iphone.card_"+key, args...) }
	what := cardTarget(l, target, at)
	switch action {
	case "tap", "long_press", "drag", "swipe":
		if what == "" {
			return t("act_" + action)
		}
		return t("act_"+action+"_on", i18n.Args{"target": what})
	case "type":
		var typed string
		switch {
		case text != "":
			typed = t("act_type_text", i18n.Args{"text": clipRunes(text, iphoneCardText)})
		case textLen > 0 && withText:
			typed = t("act_type_hidden", i18n.Args{"count": strconv.Itoa(textLen)})
		case textLen > 0:
			typed = t("act_type_count", i18n.Args{"count": strconv.Itoa(textLen)})
		default:
			typed = t("act_type")
		}
		if what == "" {
			return typed
		}
		return t("act_type_into", i18n.Args{"typed": typed, "target": what})
	case "key":
		return t("act_key", i18n.Args{"key": key})
	case "button":
		switch button {
		case "home", "lock", "volume_up", "volume_down":
			return t("act_button_" + button)
		}
		return t("act_button", i18n.Args{"button": button})
	case "launch", "terminate":
		return t("act_"+action, i18n.Args{"app": app})
	case "wait":
		return t("act_wait")
	}
	return action
}

// cardTarget is what a step acts on: an element by its label, else its number; a point when there are no elements.
func cardTarget(l i18n.Lang, target *proto.IPhoneElementRef, at *proto.IPhonePoint) string {
	t := func(key string, args ...i18n.Args) string { return i18n.T(l, "hub-iphone.card_"+key, args...) }
	switch {
	case target != nil && strings.TrimSpace(target.Label) != "":
		return t("target_label", i18n.Args{"label": clipRunes(strings.TrimSpace(target.Label), 60)})
	case target != nil && target.N > 0:
		return t("target_n", i18n.Args{"n": strconv.Itoa(target.N)})
	case at != nil:
		return t("target_at", i18n.Args{"x": strconv.Itoa(int(at.X + 0.5)), "y": strconv.Itoa(int(at.Y + 0.5))})
	}
	return ""
}

func cardApp(a proto.IPhoneApp) string {
	if n := strings.TrimSpace(a.Name); n != "" {
		return n
	}
	return strings.TrimSpace(a.BundleID)
}

// clipRunes is s cut to most characters, with an ellipsis when cut.
func clipRunes(s string, most int) string {
	if utf8.RuneCountInString(s) <= most {
		return s
	}
	return string([]rune(s)[:most]) + "…"
}
