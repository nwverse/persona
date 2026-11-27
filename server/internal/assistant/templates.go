package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"github.com/nwverse/persona/server/internal/apierr"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Templates (#16772, docs/protocol/protocol.md "Templates"): a template is a named set of configuration the owner applies
// in one step from Settings › General › Templates. The apps apply its browser, navigation and extension parts through
// their own routes; its `settings` part is persona_settings keys, which only the manager can check and write. This route
// is that part: each item goes through the same setter persona_settings uses (its validation, its before → after words
// and its write), so a template can carry any of the assistant's own settings without a second copy of their rules.
//
// It is the owner's own action from the settings page (a full device, as every other settings write), so it takes no
// card; `dry_run` answers what each item would do first, and the app shows that before the owner applies it. Items run
// in order and independently: one that fails does not stop the rest, and each answers what happened to it.

// maxTemplateSettings bounds one template's settings part.
const maxTemplateSettings = 64

// templateExcludedKeys are settings a template may not carry: they grant permissions or speak for the owner, and stay
// changes the owner makes one by one (standing authorizations only from the main conversation; the outward-message
// identity because it decides who messages are sent as).
var templateExcludedKeys = map[string]bool{standingAuthorizationsKey: true, outboundIdentityKey: true}

type templateSettingItem struct {
	Key    string          `json:"key"`
	Action string          `json:"action,omitempty"` // set (the default), add or remove, as the setting takes
	Value  json.RawMessage `json:"value"`
}

type templateSettingsRequest struct {
	Settings []templateSettingItem `json:"settings"`
	DryRun   bool                  `json:"dry_run"`
}

// Template setting statuses: what the item would do (change, unchanged) on a dry run, what it did (applied, unchanged,
// failed) otherwise, or why it was left alone (unsupported: not a setting this manager writes; not_allowed: excluded
// above; invalid: its setter refused the value).
const (
	templateChange      = "change"
	templateUnchanged   = "unchanged"
	templateApplied     = "applied"
	templateFailed      = "failed"
	templateUnsupported = "unsupported"
	templateNotAllowed  = "not_allowed"
	templateInvalid     = "invalid"
)

type templateSettingResult struct {
	Key    string `json:"key"`
	Action string `json:"action"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status"`
	// Text is the change in the owner's words (the card persona_settings would show) for change, or what the setter
	// said for applied and unchanged.
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}

func (s *Service) templateRoutes() {
	s.handle("POST /api/v1/templates/settings", s.postTemplateSettings)
}

func (s *Service) postTemplateSettings(w http.ResponseWriter, r *http.Request) {
	if !fullRole(r) {
		apierr.Write(w, http.StatusForbidden, errors.New("only a device with full access may apply a template"))
		return
	}
	var body templateSettingsRequest
	if err := decodeBody(w, r, &body, 256<<10); err != nil || body.Settings == nil {
		writeBodyErr(w, err, errors.New("settings is required"))
		return
	}
	if len(body.Settings) > maxTemplateSettings {
		apierr.Write(w, http.StatusBadRequest, errors.New("a template carries at most 64 settings"))
		return
	}
	ctx := r.Context()
	l := s.lang(ctx)
	results := make([]templateSettingResult, 0, len(body.Settings))
	for _, item := range body.Settings {
		res := templateSettingResult{Key: item.Key, Action: item.Action}
		if res.Action == "" {
			res.Action = "set"
		}
		o, found, err := s.lookupManagedOwnSetting(ctx, item.Key)
		_, direction := directionPromptSettingID(item.Key)
		switch {
		case err != nil:
			res.Status, res.Error = templateFailed, err.Error()
		case templateExcludedKeys[item.Key] || direction:
			res.Status = templateNotAllowed
		case !found:
			res.Status = templateUnsupported
		case !slices.Contains([]string{"set", "add", "remove"}, res.Action):
			res.Status, res.Error = templateInvalid, "action must be set, add or remove"
		default:
			res.Title = s.ownSettingTitle(l, o)
			s.runTemplateSetting(ctx, o, item, body.DryRun, l, &res)
		}
		results = append(results, res)
	}
	apierr.WriteJSON(w, map[string]any{"results": results, "dry_run": body.DryRun})
}

// runTemplateSetting prepares one item with its setter and, unless this is a dry run, applies it.
func (s *Service) runTemplateSetting(ctx context.Context, o ownSetting, item templateSettingItem, dryRun bool, l i18n.Lang, res *templateSettingResult) {
	change, err := s.prepareOwnSettingAction(ctx, o, res.Action, item.Value)
	switch {
	case err != nil:
		res.Status, res.Error = templateInvalid, err.Error()
	case change == nil || change.apply == nil:
		res.Status = templateUnchanged
		if change != nil {
			res.Text = change.summary
		}
	case dryRun:
		res.Status, res.Text = templateChange, change.card(l)
	default:
		text, err := change.apply(ctx)
		if err != nil {
			res.Status, res.Error = templateFailed, err.Error()
			return
		}
		res.Status, res.Text = templateApplied, text
	}
}

// ownSettingTitle is a setting's name in the owner's language, as its card names it.
func (s *Service) ownSettingTitle(l i18n.Lang, o ownSetting) string {
	if o.cardTitle != nil {
		return o.cardTitle(l)
	}
	if k, ok := settingTitles[o.Key]; ok {
		return i18n.T(l, "assistant-settings_tool."+k)
	}
	return o.Title
}
