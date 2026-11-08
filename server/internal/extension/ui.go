package extension

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
)

// Declarative UI (docs/extensions/extensions.md "Declarative UI"): a plugin never runs code in the apps. It answers ui.render with a View
// — a few fixed kinds of items — and the apps draw it with their own components (each style's, in light and dark);
// a button's action goes back to the plugin (ui.action). The same View is drawn by iOS, Android and the web.

// UIItem is a contribution to ui: a place in the apps a plugin draws.
type UIItem struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Placement is one of UISlots: extension_page (a page opened from the extension's settings; plugin_page is its old
	// name, the extension is told the one it wrote), launcher_section (a section
	// on the page of a launcher whose gateway is one of Hosts, or of any launcher when there are none: the plugin then
	// answers unsupported for gateways not its own; it may send requests there with the key, as quota does), nav_page,
	// overview_card, capsule_card, machine_section, inbox_item.
	Placement string   `json:"placement"`
	Hosts     []string `json:"hosts,omitempty"`
	// Icon is a nav_page's icon, from UIIcons (each app draws its own); the default when empty.
	Icon string `json:"icon,omitempty"`
	// Opens is the ui (a nav_page or extension_page of the same extension) a compact card or widget opens when
	// tapped.
	Opens string `json:"opens,omitempty"`
	// Connector is the connector an inbox_item draws the items of: one of the plugin's own connectors.
	Connector string `json:"connector,omitempty"`
	// Families are the sizes a widget can be drawn at (WidgetFamilies); RefreshMinutes how often it is drawn again on
	// its own (WidgetRefreshDefault when 0). A widget's only.
	Families       []string `json:"families,omitempty"`
	RefreshMinutes int      `json:"refresh_minutes,omitempty"`
}

// Refresh is how often widget u is drawn again on its own.
func (u UIItem) Refresh() time.Duration {
	return time.Duration(cmp.Or(u.RefreshMinutes, WidgetRefreshDefault)) * time.Minute
}

// UISlot is a place in the apps plugins may draw (docs/extensions/extensions.md "UI slots").
type UISlot struct {
	Name string
	// Size is what the plugin is asked to draw there: compact (a card) or full (a page, a section).
	Size string
	// Context is what the apps say about where it is drawn: launcher, machine, item.
	Context string
	// Badge: the plugin may put a badge (ui.badge) on its entry.
	Badge bool
	// Shown: the user is shown before installing that the plugin draws here (extension-permissions.slot_<Name>), and
	// agrees again when an upgrade adds it (Explain, Digest): it sits among the app's own pages, outside the plugin's
	// settings.
	Shown bool
	// Since is the UI version that brought it.
	Since int
}

// UISlots are the places this hub lets plugins draw, in the order the apps list them.
var UISlots = []UISlot{
	{Name: "plugin_page", Size: SizeFull, Since: 1},
	// extension_page is plugin_page's new name: the same slot (PageSlot).
	{Name: "extension_page", Size: SizeFull, Since: 3},
	{Name: "launcher_section", Size: SizeFull, Context: "launcher", Since: 1},
	{Name: "nav_page", Size: SizeFull, Badge: true, Shown: true, Since: 2},
	{Name: "overview_card", Size: SizeCompact, Shown: true, Since: 2},
	{Name: "capsule_card", Size: SizeCompact, Shown: true, Since: 3},
	{Name: "machine_section", Size: SizeFull, Context: "machine", Shown: true, Since: 2},
	// inbox_item draws the body of an inbox item from the plugin's connector (its own header stays the app's): the
	// same items a plugin's connector brought in, with the hub's triage on them. Drawn with UI version 2's items.
	{Name: "inbox_item", Size: SizeFull, Context: "item", Shown: true, Since: 2},
	// widget is drawn outside the app (docs/extensions/extensions.md "System-level places"): a home screen widget, a Lock Screen or watch
	// face complication. The size it is asked for is the family (FitWidget holds the view to it); the apps list
	// widgets on their own (GET /api/v1/extensions/-/widgets), not with the slots drawn in the app.
	{Name: "widget", Size: WidgetFamilySmall, Shown: true, Since: 3},
}

// Widgets: the families a widget is drawn at and how often it is drawn again (docs/extensions/extensions.md "System-level places").
const (
	WidgetFamilySmall    = wire.WidgetFamilySmall
	WidgetRefreshDefault = 30 // minutes
	WidgetRefreshLeast   = 15
	WidgetRefreshMost    = 24 * 60
)

// WidgetFamilies are the sizes a widget can be drawn at: small / medium / large on the home screen, accessory_* on the
// Lock Screen and watch faces.
var WidgetFamilies = wire.WidgetFamilies

// Slot is the slot named name.
func Slot(name string) (UISlot, bool) {
	i := slices.IndexFunc(UISlots, func(s UISlot) bool { return s.Name == name })
	if i < 0 {
		return UISlot{}, false
	}
	return UISlots[i], true
}

// Sizes, platforms and icons of the UI.
const (
	SizeCompact = "compact"
	SizeFull    = "full"
)

var (
	UIPlatforms = []string{"ios", "mac", "android", "web"}
	// UIIcons is the fixed icon set (a nav_page's, a row's, an empty state's): the schema's NavIcon.
	UIIcons = wire.NavIcons
)

// UIVersion is the version of the UI this hub knows: 1 the first eight items and two slots; 2 the slots of UISlots,
// chart and stat_grid, refresh_after and the render context; 3 markdown, table, empty and loading, a row's icon, avatar
// and thumbnail, components' keys, a view's search and filter, a section's more, pushing and popping pages,
// ui.invalidate; 4 the code hosting components (header, breadcrumb, file_tree, code, diff, comment_thread, composer), a
// view's tabs and split layout, an action's values (#2951); 5 code review (#3136): a diff's context lines, viewed marks,
// generated files, drafts of a pending review and its bar, resolving threads, a code's line links, and an action
// answering with lines or with components in place; 6 pictures, controls in rows, candlestick, area, donut and pie
// charts and sparks, a row's extra actions, grids and carousels, date, time, multiple choice and multi-line fields,
// title styles, named colors and more icons (#5464). An app says which it draws (context.ui_version, 1 when it says
// nothing); the hub leaves out of a View what the app does not know, or draws it with what it knows (Fit).
const UIVersion = 6

// PageSlot says whether placement is the extension's own page: extension_page, or plugin_page as it was called.
func PageSlot(placement string) bool {
	return placement == "extension_page" || placement == "plugin_page"
}

// FullPage says whether placement is a whole page a push may open: an extension_page or a nav_page.
func FullPage(placement string) bool { return PageSlot(placement) || placement == "nav_page" }

// UIMachine is the machine a machine_section is drawn for.
type UIMachine struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	OS     string `json:"os,omitempty"`
	Online bool   `json:"online"`
}

// UIInboxItem is the inbox item an inbox_item is drawn for: the row as the hub keeps it. Meta is the connector's own
// record of what the item gathers, passed as it is stored (null when it has none): the hub never reads it. Extra are
// the item's source blocks as the apps get them (lark, doc, moment, connector…), for a plugin drawing an item that has
// no meta.
type UIInboxItem struct {
	ID      string                     `json:"id"`
	Title   string                     `json:"title"`
	Summary string                     `json:"summary,omitempty"`
	Tier    string                     `json:"tier,omitempty"`
	Reason  string                     `json:"reason,omitempty"`
	Done    bool                       `json:"done"`
	Meta    json.RawMessage            `json:"meta"`
	Extra   map[string]json.RawMessage `json:"extra,omitempty"`
	// Count is how many messages the item holds (for an event, the hub's own count); StartedAt is when it began.
	Count     int       `json:"count,omitempty"`
	StartedAt time.Time `json:"started_at,omitzero"`
}

// UILauncher is the launcher a launcher_section is drawn for; QueryID lets the plugin reach its gateway
// (gateway.http) while it draws.
type UILauncher struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	QueryID string `json:"query_id"`
}

// UIParams are ui.render's and ui.action's parameters: what to draw and where, for whom.
type UIParams struct {
	Contribution string            `json:"contribution"`
	State        string            `json:"state"`
	Action       string            `json:"action,omitempty"`
	Values       map[string]string `json:"values,omitempty"`
	Placement    string            `json:"placement"`
	Size         string            `json:"size"`
	// UIVersion is what the app that asks draws (UIVersion at most): a plugin may draw an older app something it knows.
	UIVersion int          `json:"ui_version"`
	TZ        string       `json:"tz,omitempty"`     // the user's time zone ("Asia/Shanghai"); "" when unknown
	Locale    string       `json:"locale,omitempty"` // the app's language ("zh-Hans-CN")
	Platform  string       `json:"platform,omitempty"`
	Machine   *UIMachine   `json:"machine,omitempty"`
	Launcher  *UILauncher  `json:"launcher,omitempty"`
	Item      *UIInboxItem `json:"item,omitempty"`
}

// BadgeEvent is ui.badge, sent by a plugin: the badge on its nav_page's entry (no text clears it).
type BadgeEvent struct {
	Contribution string `json:"contribution"`
	Text         string `json:"text,omitempty"`
	Tone         string `json:"tone,omitempty"`
}

// BadgeMost is how long a badge is, in characters.
const BadgeMost = 8

// CheckBadge holds a badge to what the apps draw.
func CheckBadge(text, tone string) error {
	if utf8.RuneCountInString(text) > BadgeMost {
		return fmt.Errorf("a badge has at most %d characters", BadgeMost)
	}
	if !slices.Contains(tones, tone) {
		return errors.New("tone is neutral, positive, caution or danger")
	}
	return nil
}

// EventsItem is a contribution to events: the hub's events a plugin is told about. It is only told; it changes
// nothing (docs/extensions/extensions.md "Events").
type EventsItem struct {
	ID        string   `json:"id"`
	Subscribe []string `json:"subscribe"`
}

// ErrorReportsItem is a contribution to error_reports: the error and crash reports it is given (Sources: only those;
// none: all).
type ErrorReportsItem = wire.ErrorReportsItem

// AuthRelayItem is a contribution to auth_relay: the authorization links (hosts and paths) it completes.
type AuthRelayItem = wire.AuthRelayItem

// Events this hub has.
var (
	// EventTopics are the events by topic; how the install screen says each is extension-permissions.event_<topic with
	// _ for .> (Explain). No text here: the catalogs are read on first use, not when the package starts (#1308).
	EventTopics = map[string]bool{
		"kb.changed":      true,
		"kb.forgotten":    true,
		"inbox.changed":   true,
		"agent.needs_you": true,
		"agent.finished":  true,
		"todo.changed":    true,
		"todo.decision":   true,
	}

	// EventScopes are the hub scopes an event needs besides the subscription: only an extension granted the scope
	// (or the legacy full-hub grant) is told. A topic not listed needs none.
	EventScopes = map[string]string{
		"kb.changed":    ScopeKBRead,
		"kb.forgotten":  ScopeKBRead,
		"todo.changed":  ScopeTodosRead,
		"todo.decision": ScopeTodosRead,
	}
)

// The View and its components are the wire types generated from schema/extension/view.schema.json (sdk/go/wire):
// the schema is the one truth of what an extension may draw, and the same types are every app's.
type (
	// View is what an extension draws (ui.render, ui.action): sections of components, a state of its own the apps send
	// back (at most StateMost bytes), refresh_after.
	View    = wire.View
	Section = wire.Section
	// UIElem is one component; Type says which fields mean something (the schema's Component, one flat struct).
	UIElem      = wire.Component
	ChartSeries = wire.ChartSeries
	ChartPoint  = wire.ChartPoint
	Stat        = wire.Stat
	Badge       = wire.Badge
	// UIAction is what a row, a button or a segmented does: ui.action with its ID (and the fields' values when Submit).
	UIAction = wire.Action
	UIChoice = wire.Choice
	// ActionResult is what ui.action answers: a new View, a short message, a link to open, a page to push or pop, or
	// nothing (draw again).
	ActionResult = wire.ActionResult
	// Push is a page an action opens on top: one of the extension's whole pages, with a state.
	Push = wire.Push
	// InvalidateEvent is ui.invalidate, sent by an extension: what it draws changed (one ui, or all).
	InvalidateEvent = wire.InvalidateEvent
	// UI version 4's code hosting parts (#2951): a page's tabs, a file tree's entries, a diff's hunks and threads,
	// comments, a composer's buttons, a code's highlighted lines.
	Tabs           = wire.Tabs
	Tab            = wire.Tab
	TreeEntry      = wire.TreeEntry
	Hunk           = wire.Hunk
	Thread         = wire.Thread
	Comment        = wire.Comment
	ComposerButton = wire.ComposerButton
	LineRange      = wire.LineRange
	// UI version 5's code review parts (#3136): lines of a file a diff's context asked for, the pending review's bar.
	FileLines = wire.FileLines
	Review    = wire.Review
)

// InboxSourceURL is the one link besides https an action may open, and only on an inbox_item, for the item it is
// drawn for: the apps open the item's own source page there (what opening the item showed before the slot: a Feishu
// chat at the item's first message). The id is escaped as one path segment ("lark:F1:1051" stays as it is).
func InboxSourceURL(itemID string) string {
	return "persona://inbox/" + url.PathEscape(itemID) + "/source"
}

// SettingsURL is the other link besides https an action may open, only to its own extension's settings (#5906): the
// apps open 设置 › 扩展 › that extension there (the web /settings?tab=extensions&extension=<id>).
func SettingsURL(id string) string {
	return "persona://settings/extensions/" + url.PathEscape(id)
}

// Limits of a View.
const (
	ItemsMost  = wire.ItemsMost
	StringMost = wire.StringMost
	StateMost  = wire.StateMost
	// A chart has at most SeriesMost series of PointsMost points; a stat_grid at most StatsMost stats.
	SeriesMost = wire.SeriesMost
	PointsMost = wire.PointsMost
	StatsMost  = wire.StatsMost
	// A segmented has at most SegmentsMost choices.
	SegmentsMost = 5
	// A compact view (an overview_card) has at most CompactSections sections and CompactItems items, and no fields.
	CompactSections = 2
	CompactItems    = 12
	// A view has at most StackMost pages pushed on its own (the apps keep no more).
	StackMost = 8
	// A markdown has at most MarkdownMost characters; a table at most TableColumnsMost columns and TableRowsMost rows.
	MarkdownMost     = 20000
	TableColumnsMost = 8
	TableRowsMost    = 100
	// RefreshLeast and RefreshMost bound refresh_after, in seconds.
	RefreshLeast = 30
	RefreshMost  = 24 * 3600
)

// elemSince is each component's type and the UI version that brought it (the schema's x-since).
var elemSince = wire.ComponentSince

// tones are the tones a component may have; empty is the app's own.
var tones = append([]string{""}, wire.Tones...)

func CheckView(v *View) error { return wire.CheckView(v) }

func Actions(v *View) []*UIAction { return wire.Actions(v) }

// ParseView reads and checks a View a plugin answered with.
func ParseView(raw json.RawMessage) (*View, error) {
	var v View
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	if v.Sections == nil {
		v.Sections = []Section{}
	}
	for i := range v.Sections {
		if v.Sections[i].Items == nil {
			v.Sections[i].Items = []UIElem{}
		}
	}
	return &v, CheckView(&v)
}

// Fit makes a View a plugin drew into one an app that draws UI version uiVersion can draw at slot: items of types
// this hub does not know (a plugin written for a newer hub) are left out, a section they emptied with them; those
// newer than the app are drawn with what it knows where that says the same (a markdown as text, a table as rows, an
// empty state as a text and a button, a section's more as a button, a filter as a segmented), left out where it does
// not; refresh_after is held to its bounds. The view the extension drew is checked first (CheckView), whatever the
// app draws, so that a view is refused or drawn alike for every app; then what the app gets is checked again
// (wire.CheckFitted), a compact one to its limits. It says what types it left out.
func Fit(v *View, slot UISlot, uiVersion int, languages ...i18n.Lang) (dropped []string, err error) {
	return fit(v, slot, uiVersion, false, languages...)
}

func fit(v *View, slot UISlot, uiVersion int, native bool, languages ...i18n.Lang) (dropped []string, err error) {
	l := i18n.Default
	if len(languages) > 0 {
		l = languages[0].Or(i18n.Default)
	}
	if v == nil {
		return nil, errors.New("no view")
	}
	dropped = wire.StripAssistantFields(v)
	if v.Sections == nil {
		v.Sections = []Section{}
	}
	for i := range v.Sections {
		s := &v.Sections[i]
		s.Items = slices.DeleteFunc(s.Items, func(e UIElem) bool {
			if _, known := elemSince[e.Type]; known {
				return false
			}
			if !slices.Contains(dropped, e.Type) {
				dropped = append(dropped, e.Type)
			}
			return true
		})
	}
	switch {
	case v.RefreshAfter < 0:
		v.RefreshAfter = 0
	case v.RefreshAfter > 0 && v.RefreshAfter < RefreshLeast:
		v.RefreshAfter = RefreshLeast
	case v.RefreshAfter > RefreshMost:
		v.RefreshAfter = RefreshMost
	}
	if !native {
		if err := CheckView(v); err != nil {
			return dropped, err
		}
	}
	compact := slot.Size == SizeCompact
	if compact {
		if err := checkCompact6(v); err != nil {
			return dropped, err
		}
	}
	if uiVersion < 6 {
		downgrade6(v, uiVersion, compact, l)
	}
	if uiVersion < 5 {
		downgrade5(v, l)
	}
	if uiVersion < 4 {
		downgrade4(v, l)
	}
	if uiVersion < 3 {
		downgrade(v, uiVersion, l)
	}
	sections := v.Sections[:0]
	for _, s := range v.Sections {
		items := s.Items[:0]
		for _, e := range s.Items {
			since, known := elemSince[e.Type]
			if !known || since > uiVersion {
				if !slices.Contains(dropped, e.Type) {
					dropped = append(dropped, e.Type)
				}
				continue
			}
			items = append(items, e)
		}
		if len(items) == 0 && len(s.Items) > 0 && s.Footer == "" {
			continue
		}
		if s.Items = items; s.Items == nil {
			s.Items = []UIElem{}
		}
		sections = append(sections, s)
	}
	v.Sections = sections
	if !native {
		if err := wire.CheckFitted(v); err != nil {
			return dropped, err
		}
	}
	if compact {
		if v.Search != nil || v.Filter != nil || slices.ContainsFunc(v.Sections, func(s Section) bool { return s.More != nil }) {
			return dropped, errors.New("a card has no search, filter or more: open a page for them")
		}
		n := 0
		for _, s := range v.Sections {
			n += len(s.Items)
			for _, e := range s.Items {
				n += max(len(e.Tiles)-1, 0)
				if e.Type == "field" || e.Type == "composer" {
					return dropped, errors.New("a card has no fields: open a page for them")
				}
			}
		}
		if len(v.Sections) > CompactSections || n > CompactItems {
			return dropped, fmt.Errorf("a card has at most %d sections and %d items", CompactSections, CompactItems)
		}
	}
	return dropped, nil
}

// Limits of what downgrade4 draws an older app: rows of a file tree, comments of a thread, texts of code.
const (
	oldTreeRows = 60
	oldComments = 30
	oldCodeText = 8
	// oldFilterChoices is how many choices a filter had before UI version 4.
	oldFilterChoices = 20
)

// newIcons are the icons of UI version 4: an older app gets none (its default).
var newIcons = []string{"branch", "commit", "code", "pull_request", "merge", "file"}

// downgrade4 draws UI version 4's components and fields with version 3's, in place (and then downgrade with older
// ones): tabs as a segmented, a split view as one column, a header as a text and rows, code and diffs as code texts, a
// file tree as rows, a thread as rows and markdown, a composer as a field and buttons. An action's values are left out
// (an older app sends none). What does not fit in ItemsMost is cut, with a word saying so.
func downgrade4(v *View, l i18n.Lang) {
	if t := v.Tabs; t != nil {
		choices := make([]UIChoice, 0, SegmentsMost)
		for _, it := range t.Items {
			if len(choices) < SegmentsMost || it.Value == t.Value {
				title := it.Title
				if it.Badge != "" {
					title = cmp.Or(title, it.Value) + " " + it.Badge
				}
				choices = append(choices, UIChoice{Value: it.Value, Title: clip(title, 12)})
			}
		}
		if len(choices) > SegmentsMost {
			choices = append(choices[:SegmentsMost-1], choices[len(choices)-1])
		}
		a := t.Action
		v.Sections = append([]Section{{Items: []UIElem{{Type: "segmented", Name: t.Name, Choices: choices, Value: t.Value, Action: &a}}}}, v.Sections...)
	}
	if f := v.Filter; f != nil && len(f.Choices) > oldFilterChoices {
		choices := slices.Clone(f.Choices[:oldFilterChoices-1])
		if i := slices.IndexFunc(f.Choices, func(c UIChoice) bool { return c.Value == f.Value }); i >= oldFilterChoices-1 {
			choices = append(choices, f.Choices[i])
		} else {
			choices = append(choices, f.Choices[oldFilterChoices-1])
		}
		f.Choices = choices
	}
	v.Tabs, v.Layout = nil, ""
	for _, a := range Actions(v) {
		a.Values = nil
	}
	oldIcon := func(icon string) string {
		if slices.Contains(newIcons, icon) {
			return ""
		}
		return icon
	}
	codeTexts := func(text string) []UIElem {
		var out []UIElem
		for text != "" && len(out) < oldCodeText {
			chunk := text
			if utf8.RuneCountInString(chunk) > StringMost {
				chunk = string([]rune(chunk)[:StringMost])
				if i := strings.LastIndexByte(chunk, '\n'); i > 0 {
					chunk = chunk[:i+1]
				}
			}
			text = text[len(chunk):]
			if t := strings.TrimRight(chunk, "\n"); t != "" {
				out = append(out, UIElem{Type: "text", Text: t, Style: "code"})
			}
		}
		if text != "" {
			out = append(out, UIElem{Type: "text", Text: i18n.T(l, "extension-ui-fallback.truncated"), Style: "caption"})
		}
		return out
	}
	commentItems := func(c Comment) []UIElem {
		row := UIElem{Type: "row", Title: clip(c.Author, StringMost), Badge: c.Badge}
		if !c.At.IsZero() {
			row.Value = c.At.Format("2006-01-02 15:04")
		}
		out := []UIElem{row}
		if body := strings.TrimSpace(c.Body); body != "" {
			if c.Event {
				out = []UIElem{{Type: "text", Text: clip(c.Author+" "+body, StringMost), Style: "caption"}}
			} else {
				out = append(out, UIElem{Type: "markdown", Text: clip(body, MarkdownMost)})
			}
		}
		return out
	}
	total := 0
	for i := range v.Sections {
		s := &v.Sections[i]
		s.Pane = ""
		items := make([]UIElem, 0, len(s.Items))
		for _, e := range s.Items {
			e.Icon = oldIcon(e.Icon)
			switch e.Type {
			case "header":
				text := e.Title
				var badges []string
				for _, b := range e.Badges {
					badges = append(badges, b.Text)
				}
				if len(badges) > 0 {
					text += "\n" + strings.Join(badges, " · ")
				}
				if e.Subtitle != "" {
					text += "\n" + e.Subtitle
				}
				items = append(items, UIElem{Type: "text", Text: clip(text, StringMost)})
				for _, f := range e.Facts {
					row := UIElem{Type: "row", Title: f.Value, Icon: oldIcon(f.Icon)}
					if f.Label != "" {
						row.Title, row.Value = f.Label, f.Value
					}
					items = append(items, row)
				}
			case "breadcrumb":
				var parts []string
				for _, c := range e.Crumbs {
					parts = append(parts, c.Title)
				}
				items = append(items, UIElem{Type: "text", Text: clip(strings.Join(parts, " / "), StringMost), Style: "secondary"})
			case "file_tree":
				for k, en := range e.Entries {
					if k == oldTreeRows {
						items = append(items, UIElem{Type: "text", Text: i18n.T(l, "extension-ui-fallback.files_remaining", i18n.Args{"count": len(e.Entries) - k}), Style: "caption"})
						break
					}
					row := UIElem{Type: "row", Title: clip(en.Path, StringMost), Icon: "doc"}
					if en.Kind == "dir" {
						row.Icon = "folder"
					}
					if en.Additions > 0 || en.Deletions > 0 {
						row.Value = fmt.Sprintf("+%d −%d", en.Additions, en.Deletions)
					}
					if en.Badge != nil {
						row.Badge = en.Badge
					} else if t, ok := fileStatusBadge[en.Status]; ok {
						t.Text = i18n.T(l, t.Text)
						row.Badge = &t
					}
					items = append(items, row)
				}
			case "code":
				if e.Path != "" {
					items = append(items, UIElem{Type: "text", Text: clip(e.Path, StringMost), Style: "secondary"})
				}
				items = append(items, codeTexts(e.Text)...)
			case "diff":
				row := UIElem{Type: "row", Title: clip(e.Path, StringMost), Icon: "doc"}
				if e.OldPath != "" && e.OldPath != e.Path {
					row.Subtitle = clip(e.OldPath+" →", StringMost)
				}
				row.Value = fmt.Sprintf("+%d −%d", e.Additions, e.Deletions)
				if t, ok := fileStatusBadge[e.Status]; ok {
					t.Text = i18n.T(l, t.Text)
					row.Badge = &t
				}
				items = append(items, row)
				var b strings.Builder
				for _, h := range e.Hunks {
					b.WriteString(cmp.Or(h.Header, fmt.Sprintf("@@ -%d +%d @@", h.OldStart, h.NewStart)))
					b.WriteByte('\n')
					for _, l := range h.Lines {
						b.WriteString(l)
						b.WriteByte('\n')
					}
				}
				switch {
				case e.Binary:
					items = append(items, UIElem{Type: "text", Text: i18n.T(l, "extension-ui-fallback.binary"), Style: "caption"})
				case b.Len() > 0:
					items = append(items, codeTexts(b.String())...)
				}
				for _, t := range e.Threads {
					if len(t.Comments) == 0 {
						continue
					}
					c := t.Comments[0]
					items = append(items, UIElem{Type: "text", Style: "secondary",
						Text: clip(i18n.T(l, "extension-ui-fallback.line_comment", i18n.Args{"line": t.Line, "author": c.Author, "body": strings.TrimSpace(c.Body)}), StringMost)})
				}
			case "comment_thread":
				if e.Title != "" {
					items = append(items, UIElem{Type: "text", Text: e.Title, Style: "secondary"})
				}
				for k, c := range e.Comments {
					if k == oldComments {
						items = append(items, UIElem{Type: "text", Text: i18n.T(l, "extension-ui-fallback.comments_remaining", i18n.Args{"count": len(e.Comments) - k}), Style: "caption"})
						break
					}
					items = append(items, commentItems(c)...)
				}
			case "composer":
				items = append(items, UIElem{Type: "field", Name: e.Name, Label: cmp.Or(e.Placeholder, i18n.T(l, "extension-ui-fallback.comment")), Kind: "text", Value: e.Value})
				for _, b := range e.Buttons {
					a := b.Action
					a.Submit = true
					role := ""
					if b.Tone == "danger" {
						role = "destructive"
					}
					items = append(items, UIElem{Type: "button", Title: b.Title, Action: &a, Role: role})
				}
			default:
				items = append(items, e)
			}
		}
		if total+len(items) > ItemsMost-1 {
			items = append(items[:max(ItemsMost-1-total, 0)], UIElem{Type: "text", Text: i18n.T(l, "extension-ui-fallback.too_long"), Style: "caption"})
			s.Items = items
			v.Sections = v.Sections[:i+1]
			return
		}
		total += len(items)
		s.Items = items
	}
}

// downgrade5 leaves out UI version 5's fields (code review, #3136), in place: a diff's context, viewed marks, generated
// files, drafts and suggestions, a thread's resolve, a code's line link. A draft thread's first comment gets a badge
// saying so; the pending review's bar becomes a section on top: a line saying how many drafts, its composer and discard
// as a button.
func downgrade5(v *View, l i18n.Lang) {
	if r := v.Review; r != nil {
		items := []UIElem{
			{Type: "text", Text: i18n.T(l, "extension-ui-fallback.review_drafts", i18n.Args{"count": r.Count}), Style: "secondary"},
			{Type: "composer", Name: r.Name, Placeholder: r.Placeholder, Buttons: r.Buttons},
		}
		if r.Discard != nil {
			items = append(items, UIElem{Type: "button", Title: i18n.T(l, "extension-ui-fallback.discard"), Action: r.Discard, Role: "destructive"})
		}
		v.Sections = append([]Section{{Items: items}}, v.Sections...)
		v.Review = nil
	}
	for i := range v.Sections {
		for j := range v.Sections[i].Items {
			e := &v.Sections[i].Items[j]
			e.Context, e.MarkViewed, e.Draft = nil, nil, nil
			e.Viewed, e.Generated, e.Suggestions = false, false, false
			e.Revision, e.Similarity, e.LineURL = "", 0, ""
			for k := range e.Entries {
				e.Entries[k].Viewed, e.Entries[k].Generated = false, false
			}
			for k := range e.Threads {
				t := &e.Threads[k]
				if t.Pending && len(t.Comments) > 0 && t.Comments[0].Badge == nil {
					t.Comments[0].Badge = &Badge{Text: i18n.T(l, "extension-ui-fallback.draft"), Tone: "caution"}
				}
				t.Pending, t.Resolve = false, nil
			}
		}
	}
}

// FitResult makes what ui.action answered something the app can use, past the view (Fit): lines and components in
// place (UI version 5) are checked and the components fitted one by one; an older app gets neither, so it draws the
// page again.
func FitResult(res *ActionResult, slot UISlot, uiVersion int, languages ...i18n.Lang) error {
	l := i18n.Default
	if len(languages) > 0 {
		l = languages[0].Or(i18n.Default)
	}
	if uiVersion < 5 {
		res.Lines, res.Items = nil, nil
		return nil
	}
	if err := wire.CheckLines(res.Lines); err != nil {
		return err
	}
	if len(res.Items) == 0 {
		return nil
	}
	v := View{Sections: []Section{{Items: res.Items}}}
	for _, e := range res.Items {
		if e.Key == "" {
			return errors.New("items: each has the key of the one it stands for")
		}
	}
	if _, err := Fit(&v, slot, uiVersion, l); err != nil {
		return err
	}
	if len(v.Sections) == 0 || len(v.Sections[0].Items) != len(res.Items) {
		return errors.New("items: of types this app draws")
	}
	res.Items = v.Sections[0].Items
	return nil
}

// fileStatusBadge is how an older app shows a file's status.
var fileStatusBadge = map[string]Badge{
	"added":    {Text: "extension-ui-fallback.file_added", Tone: "positive"},
	"modified": {Text: "extension-ui-fallback.file_modified", Tone: "neutral"},
	"deleted":  {Text: "extension-ui-fallback.file_deleted", Tone: "danger"},
	"renamed":  {Text: "extension-ui-fallback.file_renamed", Tone: "caution"},
}

// downgrade draws v3's components and fields with those of uiVersion (1 or 2), in place: what an older app gets.
func downgrade(v *View, uiVersion int, l i18n.Lang) {
	var filter []UIElem
	if f := v.Filter; f != nil && uiVersion >= 2 && len(f.Choices) <= SegmentsMost &&
		!slices.ContainsFunc(f.Choices, func(c UIChoice) bool { return utf8.RuneCountInString(cmp.Or(c.Title, c.Value)) > 12 }) {
		filter = []UIElem{{Type: "segmented", Name: f.Name, Choices: f.Choices, Value: f.Value, Action: &f.Action}}
	}
	v.Search, v.Filter = nil, nil
	if len(filter) > 0 {
		v.Sections = append([]Section{{Items: filter}}, v.Sections...)
	}
	for i := range v.Sections {
		s := &v.Sections[i]
		items := make([]UIElem, 0, len(s.Items))
		for _, e := range s.Items {
			e.Key, e.Icon, e.Avatar, e.Thumbnail = "", "", nil, nil
			if e.Action != nil {
				a := *e.Action
				a.Push = nil
				e.Action = &a
			}
			switch e.Type {
			case "markdown":
				items = append(items, UIElem{Type: "text", Text: clip(e.Text, StringMost)})
			case "table":
				for _, r := range e.Rows {
					if len(r) == 0 {
						continue
					}
					items = append(items, UIElem{Type: "row", Title: cmp.Or(clip(r[0], StringMost), "—"), Value: clip(strings.Join(r[1:], " · "), StringMost)})
				}
				if e.Caption != "" {
					items = append(items, UIElem{Type: "text", Text: e.Caption, Style: "caption"})
				}
			case "empty":
				items = append(items, UIElem{Type: "text", Text: clip(strings.TrimSpace(e.Title+"\n"+e.Detail), StringMost), Style: "secondary"})
				if b := e.Button; b != nil {
					a := b.Action
					a.Push = nil
					items = append(items, UIElem{Type: "button", Title: b.Title, Action: &a})
				}
			case "loading":
				items = append(items, UIElem{Type: "text", Text: cmp.Or(e.Title, i18n.T(l, "extension-ui-fallback.loading")), Style: "secondary"})
			default:
				items = append(items, e)
			}
		}
		if m := s.More; m != nil {
			a := m.Action
			a.Push = nil
			items = append(items, UIElem{Type: "button", Title: cmp.Or(m.Title, i18n.T(l, "extension-ui-fallback.load_more")), Action: &a})
		}
		s.More, s.Items = nil, items
	}
}

// Limits of a widget (FitWidget): the items of each family, the stats of a stat_grid, the rows, the lines of a text.
var widgetItems = map[string]int{"small": 4, "medium": 6, "large": 12}

const (
	widgetSections   = 2
	widgetStats      = 4
	widgetStatsSmall = 2
	widgetRows       = 4
	widgetLines      = 3
	widgetText       = 200
)

// widgetKinds are the components a widget draws; the rest are left out, or drawn as text (markdown, empty, loading).
var widgetKinds = []string{"metric", "stat_grid", "progress", "chart", "text", "row"}

// FitWidget makes a View an extension drew for a widget into one the family draws (docs/extensions/extensions.md "System-level places"):
// first as any view (Fit), then held to a widget's subset — a metric, a stat_grid, a progress, a chart, a few lines of
// text, a few rows, nothing to tap (a tap opens the widget's page), no search, filter or more — cutting what is too
// long rather than refusing it. A complication (accessory_*) draws one metric, progress or line of text, the first.
// It says what it left out.
func FitWidget(v *View, family string, uiVersion int, languages ...i18n.Lang) (dropped []string, err error) {
	l := i18n.Default
	if len(languages) > 0 {
		l = languages[0].Or(i18n.Default)
	}
	if !slices.Contains(WidgetFamilies, family) {
		return nil, fmt.Errorf("a widget is drawn at one of %s", strings.Join(WidgetFamilies, ", "))
	}
	slot, _ := Slot("widget")
	// a widget draws none of UI version 6 yet: it gets what an app of version 5 gets
	if dropped, err = Fit(v, slot, min(uiVersion, 5), l); err != nil {
		return dropped, err
	}
	drop := func(what string) {
		if !slices.Contains(dropped, what) {
			dropped = append(dropped, what)
		}
	}
	if v.Search != nil || v.Filter != nil {
		drop("search")
	}
	v.Search, v.Filter, v.RefreshAfter = nil, nil, 0
	complication := strings.HasPrefix(family, "accessory_")
	stats := widgetStats
	if family == "small" {
		stats = widgetStatsSmall
	}
	sections := []Section{}
	n, rows := 0, 0
	for _, s := range v.Sections {
		if s.More != nil {
			drop("more")
		}
		var kept []UIElem
		for _, e := range s.Items {
			switch e.Type {
			case "markdown":
				e = UIElem{Type: "text", Text: e.Text}
			case "empty":
				e = UIElem{Type: "text", Text: strings.TrimSpace(e.Title + "\n" + e.Detail), Style: "secondary"}
			case "loading":
				e = UIElem{Type: "text", Text: cmp.Or(e.Title, i18n.T(l, "extension-ui-fallback.loading")), Style: "secondary"}
			}
			if !slices.Contains(widgetKinds, e.Type) {
				drop(e.Type)
				continue
			}
			e.Action, e.Avatar, e.Thumbnail = nil, nil, nil
			if complication {
				if e.Type == "metric" || e.Type == "text" || e.Type == "progress" && family != "accessory_inline" {
					if e.Type == "text" {
						e.Text = widgetLinesOf(e.Text, map[bool]int{true: widgetLines, false: 1}[family == "accessory_rectangular"])
					}
					v.Sections = []Section{{Items: []UIElem{e}}}
					return dropped, nil
				}
				continue
			}
			switch e.Type {
			case "text":
				e.Text = widgetLinesOf(e.Text, widgetLines)
			case "stat_grid":
				if len(e.Stats) > stats {
					e.Stats = e.Stats[:stats]
				}
			case "row":
				if rows++; rows > widgetRows {
					continue
				}
			}
			if n < widgetItems[family] {
				kept = append(kept, e)
				n++
			}
		}
		if len(kept) > 0 && len(sections) < widgetSections {
			sections = append(sections, Section{Title: s.Title, Items: kept})
		}
	}
	if complication {
		return dropped, errors.New("a complication draws a metric, a progress or a line of text: the view has none")
	}
	if len(sections) == 0 {
		return dropped, errors.New("a widget draws a metric, a stat_grid, a progress, a chart, text or rows: the view has none")
	}
	v.Sections = sections
	return dropped, nil
}

// widgetLinesOf is text cut to at most lines lines and widgetText characters.
func widgetLinesOf(text string, lines int) string {
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		text = strings.Join(parts[:lines], "\n") + "…"
	}
	return clip(text, widgetText)
}

// clip is s cut to at most n characters, an ellipsis ending one that was longer.
func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n-1]) + "…"
}
