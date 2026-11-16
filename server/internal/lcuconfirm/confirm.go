// Package lcuconfirm maps an LCU (amontlabs/lcu) MCP elicitation onto the permission and question cards Persona
// already shows, and maps the user's answer back. Nothing here accepts a request by itself: a missing, dismissed,
// or assistant-written answer cancels, which is what LCU treats as "the person did not approve".
package lcuconfirm

import (
	"encoding/json"
	"strings"

	"github.com/nwverse/persona/server/internal/proto"
)

// Kinds of elicitation the bridge knows. Anything else is "other": the user still sees a card, and allowing it
// does not become an accept, because the schema was not one the card can fill in.
const (
	KindNative = "native_app"
	KindOrigin = "browser_origin"
	KindURL    = "url"
	KindForm   = "form"
	KindEnum   = "enum"
	KindFields = "fields"
	KindOther  = "other"
)

// native choice values, the ones the original runtime's elicitation understands (LCU adapters/client.mjs).
const (
	choiceOnce    = "once"
	choiceSession = "session"
	choiceAlways  = "always"
	choiceDecline = "decline"
)

type parsed struct {
	kind     string
	message  string
	appName  string
	appID    string
	scopes   []string // persist scopes the runtime offered, "session" and "always" only
	risk     string
	warning  string
	origin   string
	url      string
	enumKey  string
	enumVals []string
	fields   []proto.Field
}

// NativeApp is the app a native-app elicitation asks about, its name and bundle id as the runtime gave them; ok is
// false for any other kind.
func NativeApp(params json.RawMessage) (name, id string, ok bool) {
	p := parse(params)
	return p.appName, p.appID, p.kind == KindNative
}

// Decline is the elicitation result that refuses the request without a card (an app never operated, #4336).
func Decline() map[string]any { return decline() }

// Kind is the elicitation's kind for a log line (never the message).
func Kind(params json.RawMessage) string { return parse(params).kind }

// Card is the interaction the phone and the web already know how to show. The hub fills in its id, machine,
// session and expiry. A native-app request that offers both "this session" and "always" is a question, because
// the permission card has one "always" button; every other confirmation is a permission card, the same one as
// app approval and "ask before acting".
func Card(params json.RawMessage) proto.Interaction {
	p := parse(params)
	ix := proto.Interaction{Kind: proto.IxPermission, ToolName: "操控电脑", SessionTitle: "Persona", Summary: summary(p)}
	if p.warning != "" {
		ix.Reason = rewrite(p.warning)
	}
	switch {
	case p.kind == KindNative && has(p.scopes, choiceSession) && has(p.scopes, choiceAlways):
		ix.Kind = proto.IxQuestion
		ix.Questions = []proto.Question{{
			Question: ix.Summary,
			Header:   "操控电脑",
			Options: []proto.QuestionOption{
				{Label: "允许一次", Description: "只这一次"},
				{Label: "本会话允许", Description: "这个对话里不再问这个 App"},
				{Label: "一直允许", Description: "以后在这台 Mac 上不再问"},
				{Label: "拒绝"},
			},
		}}
	case p.kind == KindNative && has(p.scopes, choiceAlways):
		ix.AlwaysLabel = "一直允许" + quoted(p.appName)
	case p.kind == KindNative && has(p.scopes, choiceSession):
		ix.AlwaysLabel = "本会话允许" + quoted(p.appName)
	case p.kind == KindEnum:
		ix.Kind = proto.IxQuestion
		opts := make([]proto.QuestionOption, 0, len(p.enumVals)+1)
		for _, v := range p.enumVals {
			opts = append(opts, proto.QuestionOption{Label: v})
		}
		opts = append(opts, proto.QuestionOption{Label: "拒绝"})
		ix.Questions = []proto.Question{{Question: ix.Summary, Header: "操控电脑", Options: opts}}
	case p.kind == KindFields:
		ix.Fields = p.fields
	}
	return ix
}

// Answer is the MCP elicitation result for what the user did with the card. The assistant answering for the user,
// a dismissal, an unknown choice, or a card this package cannot fill all cancel: the runtime then refuses the
// action, and the model is not told that it was approved.
func Answer(params json.RawMessage, res proto.Resolution) map[string]any {
	if res.AnsweredBy == proto.AnsweredByAssistant || res.Dismiss {
		return cancel()
	}
	p := parse(params)
	switch p.kind {
	case KindNative:
		return nativeAnswer(p, res)
	case KindEnum:
		return enumAnswer(p, res)
	case KindOrigin, KindForm:
		if allow, ok := allowed(res); ok {
			if allow {
				return accept(nil)
			}
			return decline()
		}
		return cancel()
	case KindURL:
		if allow, ok := allowed(res); ok {
			if allow {
				return map[string]any{"action": "accept"}
			}
			return decline()
		}
		return cancel()
	case KindFields:
		if allow, ok := allowed(res); ok {
			if !allow {
				return decline()
			}
			content := map[string]any{}
			for _, f := range p.fields {
				v := strings.TrimSpace(res.Values[f.Key])
				if v == "" && !f.Optional {
					return cancel()
				}
				if v != "" {
					content[f.Key] = v
				}
			}
			return accept(content)
		}
		return cancel()
	default:
		// Shown, and refused even if the card was allowed: the schema was not one a card can answer.
		if allow, ok := allowed(res); ok && !allow {
			return decline()
		}
		return cancel()
	}
}

func nativeAnswer(p parsed, res proto.Resolution) map[string]any {
	if p.kind == KindNative && has(p.scopes, choiceSession) && has(p.scopes, choiceAlways) {
		switch choiceLabel(res) {
		case "允许一次":
			return accept(map[string]any{})
		case "本会话允许":
			return acceptPersist(choiceSession)
		case "一直允许":
			return acceptPersist(choiceAlways)
		case "拒绝":
			return decline()
		default:
			return cancel()
		}
	}
	allow, ok := allowed(res)
	if !ok {
		return cancel()
	}
	if !allow {
		return decline()
	}
	if res.Always && has(p.scopes, choiceAlways) {
		return acceptPersist(choiceAlways)
	}
	if res.Always && has(p.scopes, choiceSession) {
		return acceptPersist(choiceSession)
	}
	// "Allow", and "always" when the runtime did not offer a longer scope: once, with no persist.
	return accept(map[string]any{})
}

func enumAnswer(p parsed, res proto.Resolution) map[string]any {
	label := choiceLabel(res)
	if label == "" || label == "拒绝" {
		if label == "拒绝" {
			return decline()
		}
		return cancel()
	}
	for _, v := range p.enumVals {
		if v == label {
			return accept(map[string]any{p.enumKey: v})
		}
	}
	return cancel()
}

func choiceLabel(res proto.Resolution) string {
	for _, v := range res.Answers {
		return strings.TrimSpace(v)
	}
	return ""
}

func allowed(res proto.Resolution) (bool, bool) {
	if res.Allow == nil {
		return false, false
	}
	return *res.Allow, true
}

func accept(content map[string]any) map[string]any {
	if content == nil {
		content = map[string]any{}
	}
	return map[string]any{"action": "accept", "content": content}
}

func acceptPersist(scope string) map[string]any {
	return map[string]any{"action": "accept", "content": map[string]any{}, "_meta": map[string]any{"persist": scope}}
}

func decline() map[string]any { return map[string]any{"action": "decline"} }

func cancel() map[string]any { return map[string]any{"action": "cancel"} }

func summary(p parsed) string {
	msg := rewrite(p.message)
	switch p.kind {
	case KindNative:
		name := p.appName
		if name == "" {
			name = p.appID
		}
		if name != "" {
			msg = "允许操控" + quoted(name) + "？\n" + msg
		}
	case KindOrigin:
		msg = "允许浏览器访问 " + p.origin + "？\n" + msg
	case KindURL:
		if p.url != "" {
			msg = msg + "\n" + p.url
		}
	case KindOther:
		if msg == "" {
			msg = "电脑上的操控请求发来一项手机上还不能填写的确认。"
		}
		msg += "\n点允许也会取消这次请求。"
	}
	if p.risk == "high" {
		msg = "高风险。\n" + msg
	}
	msg = strings.TrimSpace(msg)
	if msg == "" {
		msg = "允许这次操控电脑？"
	}
	return msg
}

// rewrite is the display text only. The runtime's warning is written for its own product.
func rewrite(s string) string {
	s = strings.ReplaceAll(s, "ChatGPT", "助手")
	return strings.TrimSpace(s)
}

func quoted(name string) string {
	if name == "" {
		return ""
	}
	return "「" + name + "」"
}

func has(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

func parse(raw json.RawMessage) parsed {
	var in struct {
		Mode            string          `json:"mode"`
		Message         string          `json:"message"`
		URL             string          `json:"url"`
		RequestedSchema json.RawMessage `json:"requestedSchema"`
		Meta            json.RawMessage `json:"_meta"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil {
		return parsed{kind: KindOther}
	}
	meta := map[string]json.RawMessage{}
	_ = json.Unmarshal(in.Meta, &meta)
	if in.Mode == "url" {
		return parsed{kind: KindURL, message: in.Message, url: strings.TrimSpace(in.URL)}
	}
	if origin, ok := exactOrigin(meta); ok {
		return parsed{kind: KindOrigin, message: in.Message, origin: origin}
	}
	if n, ok := native(in.Mode, in.Message, in.RequestedSchema, meta); ok {
		return n
	}
	return schemaCard(in.Mode, in.Message, in.RequestedSchema)
}

func exactOrigin(meta map[string]json.RawMessage) (string, bool) {
	var tool, origin string
	if json.Unmarshal(meta["tool_name"], &tool) != nil || tool != "access_browser_origin" {
		return "", false
	}
	if json.Unmarshal(meta["origin"], &origin) != nil {
		return "", false
	}
	origin = strings.TrimSpace(origin)
	if !strings.HasPrefix(origin, "https://") && !strings.HasPrefix(origin, "http://") {
		return "", false
	}
	if strings.ContainsAny(origin, " \t\r\n") || strings.Count(origin, "://") != 1 {
		return "", false
	}
	rest := origin[strings.Index(origin, "://")+3:]
	if rest == "" || strings.Contains(rest, "/") || strings.Contains(rest, "?") || strings.Contains(rest, "#") {
		return "", false
	}
	return origin, true
}

func native(mode, message string, schema json.RawMessage, meta map[string]json.RawMessage) (parsed, bool) {
	var kind, connector string
	_ = json.Unmarshal(meta["codex_approval_kind"], &kind)
	_ = json.Unmarshal(meta["connector_id"], &connector)
	if mode != "form" || strings.TrimSpace(message) == "" || kind != "mcp_tool_call" || connector != "computer-use" {
		return parsed{}, false
	}
	var sch struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if json.Unmarshal(schema, &sch) != nil || sch.Type != "object" || sch.Properties == nil || len(sch.Properties) != 0 || len(sch.Required) != 0 {
		return parsed{}, false
	}
	var toolParams struct {
		App string `json:"app"`
	}
	if json.Unmarshal(meta["tool_params"], &toolParams) != nil || strings.TrimSpace(toolParams.App) == "" {
		return parsed{}, false
	}
	var persist []string
	_ = json.Unmarshal(meta["persist"], &persist)
	scopes := []string{}
	for _, s := range persist {
		if s == choiceSession || s == choiceAlways {
			scopes = append(scopes, s)
		}
	}
	var risk, warning, subtitle string
	_ = json.Unmarshal(meta["riskLevel"], &risk)
	_ = json.Unmarshal(meta["warningSubtitle"], &warning)
	_ = json.Unmarshal(meta["subtitle"], &subtitle)
	if strings.TrimSpace(warning) == "" {
		warning = subtitle
	}
	name := strings.TrimSpace(toolParams.App)
	var display []struct {
		Name  string `json:"name"`
		Value string `json:"value"`
	}
	if json.Unmarshal(meta["tool_params_display"], &display) == nil {
		for _, d := range display {
			if d.Name == "app" && strings.TrimSpace(d.Value) != "" {
				name = strings.TrimSpace(d.Value)
			}
		}
	}
	return parsed{
		kind: KindNative, message: message, appName: name, appID: toolParams.App,
		scopes: scopes, risk: strings.TrimSpace(risk), warning: strings.TrimSpace(warning),
	}, true
}

func schemaCard(mode, message string, schema json.RawMessage) parsed {
	p := parsed{kind: KindOther, message: message}
	if mode != "form" || strings.TrimSpace(message) == "" {
		return p
	}
	var sch struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if len(schema) == 0 || json.Unmarshal(schema, &sch) != nil || sch.Type != "object" {
		return p
	}
	if len(sch.Properties) == 0 {
		p.kind = KindForm
		return p
	}
	required := map[string]bool{}
	for _, k := range sch.Required {
		required[k] = true
	}
	if len(sch.Properties) == 1 {
		for key, raw := range sch.Properties {
			var prop struct {
				Type string   `json:"type"`
				Enum []string `json:"enum"`
			}
			if json.Unmarshal(raw, &prop) == nil && prop.Type == "string" && len(prop.Enum) > 0 {
				p.kind, p.enumKey, p.enumVals = KindEnum, key, prop.Enum
				return p
			}
		}
	}
	fields := make([]proto.Field, 0, len(sch.Properties))
	for key, raw := range sch.Properties {
		var prop struct {
			Type        string `json:"type"`
			Title       string `json:"title"`
			Description string `json:"description"`
		}
		if json.Unmarshal(raw, &prop) != nil {
			return p
		}
		switch prop.Type {
		case "string", "number", "integer", "boolean":
		default:
			return p
		}
		label := prop.Title
		if label == "" {
			label = prop.Description
		}
		if label == "" {
			label = key
		}
		fields = append(fields, proto.Field{Key: key, Label: label, Optional: !required[key], Secret: strings.Contains(strings.ToLower(key), "password")})
	}
	p.kind, p.fields = KindFields, fields
	return p
}
