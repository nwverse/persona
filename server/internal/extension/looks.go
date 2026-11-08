package extension

import (
	"encoding/json"
	"fmt"
	"github.com/nwverse/persona/server/internal/compat"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nwverse/persona/sdk/go/wire"
	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/lazyre"
)

// Looks (docs/extensions/extensions.md "Looks", docs/apple/persona.md): an extension adds looks — Persona's name, face, colors and the dot's
// states — by declaring them in contributes.looks, with the pictures in its package. The hub serves each as a persona
// package (format 1), so the apps show an extension's look the way they show one on the hub's disk. A look also says
// which built-in look's app icon stands for it where an icon has to be in the app already (the home screen icon).

// LookItem is a contribution to looks.
type LookItem struct {
	ID string `json:"id"`
	// Name is what the apps call it (zh-Hans, the source language); Names are its other languages (en, ja).
	Name      string            `json:"name"`
	Names     map[string]string `json:"names,omitempty"`
	Subtitle  string            `json:"subtitle,omitempty"`
	Subtitles map[string]string `json:"subtitles,omitempty"`
	Avatar    LookAvatar        `json:"avatar"`
	Colors    *LookColors       `json:"colors,omitempty"`
	Dot       *LookDot          `json:"dot,omitempty"`
	// AppIconFallback is a built-in look's id: its icon is the app icon where the platform can only use icons the app
	// ships (iOS's home screen, Android's launcher).
	AppIconFallback string `json:"app_icon_fallback"`
}

// LookAvatar is the face: square PNGs by their side in pixels, an SVG, and the same for dark mode.
type LookAvatar struct {
	Sizes map[string]string `json:"sizes"`
	SVG   string            `json:"svg,omitempty"`
	Safe  float64           `json:"safe,omitempty"`
	Dark  *LookAvatarDark   `json:"dark,omitempty"`
}

// LookAvatarDark is the face on dark backgrounds; what it leaves out is the light one's.
type LookAvatarDark struct {
	Sizes map[string]string `json:"sizes,omitempty"`
	SVG   string            `json:"svg,omitempty"`
}

// LookColors are the colors per appearance (docs/apple/persona.md "Color families"): primary, secondary, accent, bubble, bubble_text.
type LookColors struct {
	Light map[string]string `json:"light,omitempty"`
	Dark  map[string]string `json:"dark,omitempty"`
}

// LookDot is the dot's states, as persona.json's dot (an APNG or a PNG each; idle required when there are any).
type LookDot struct {
	Size   int                     `json:"size,omitempty"`
	States map[string]LookDotState `json:"states"`
}

type LookDotState struct {
	File string  `json:"file"`
	FPS  float64 `json:"fps,omitempty"`
}

// The bounds a look's files are held to (the same as a persona package's, docs/apple/persona.md "Check rules", and a picture's
// pixels): a mistake (a 4K source image left in, a script in an SVG) stays off the phones.
const (
	LookMaxTotal   = 16 << 20
	LookMaxSVG     = wire.LookMaxSVG
	LookMaxSide    = 1024 // an avatar PNG's side
	LookMaxDotSide = 512  // a dot frame's side
	LookMinSide    = wire.LookMinSide
)

// BuiltinLooks are the looks every app draws itself. Only Mochi remains; downloaded packs use its app icon.
var BuiltinLooks = []string{DefaultLook}

// RetiredLookIDs stay reserved so an extension cannot hijack a saved choice after its built-in artwork is removed.
var RetiredLookIDs = []string{"persona-visor", "persona-circuit", "persona-pixel", "persona-facet", "persona-blob"}

// ReservedLookID excludes both the current built-in drawing and retired choices from downloadable packages.
func ReservedLookID(id string) bool {
	id = compat.Looks(id)
	return slices.Contains(BuiltinLooks, id) || slices.Contains(RetiredLookIDs, id)
}

// DefaultLook is the look of one who never chose (and of one whose look went away).
const DefaultLook = "persona"

// LookColorRoles are the colors a look may give.
var LookColorRoles = []string{"primary", "secondary", "accent", "bubble", "bubble_text"}

// LookStates are the dot's states.
var LookStates = []string{"idle", "thinking", "working", "needs_you", "offline"}

// LookLanguages are the languages names may be in besides Name's: the interface languages (docs/protocol/i18n.md).
var LookLanguages = func() []string {
	out := make([]string, len(i18n.Langs))
	for i, l := range i18n.Langs {
		out[i] = string(l)
	}
	return out
}()

// lookID is a persona package's id (docs/apple/persona.md: the apps take it as a directory and cache key).
var lookID = lazyre.New(`^[a-z0-9][a-z0-9-]{0,31}$`)

func init() {
	Points["looks"] = Point{Name: "looks", Namespace: "looks", Version: 1, Item: func(raw json.RawMessage) (string, error) {
		var l LookItem
		if err := strict(raw, &l); err != nil {
			return "", err
		}
		return l.ID, l.check()
	}}
}

// check holds a look to what can be said of it without its files.
func (l LookItem) check() error {
	if !lookID.MatchString(l.ID) {
		return fmt.Errorf("look id %q: a-z, 0-9 and -, at most 32", l.ID)
	}
	if ReservedLookID(l.ID) {
		return fmt.Errorf("look %s: a reserved look id", l.ID)
	}
	if n := utf8.RuneCountInString(strings.TrimSpace(l.Name)); n == 0 || n > 20 {
		return fmt.Errorf("look %s: name: 1 to 20 characters", l.ID)
	}
	for what, m := range map[string]map[string]string{"names": l.Names, "subtitles": l.Subtitles} {
		for lang, s := range m {
			if !slices.Contains(LookLanguages, lang) {
				return fmt.Errorf("look %s: %s.%s: only %s", l.ID, what, lang, strings.Join(LookLanguages, ", "))
			}
			if n := utf8.RuneCountInString(strings.TrimSpace(s)); n == 0 || n > 40 {
				return fmt.Errorf("look %s: %s.%s: 1 to 40 characters", l.ID, what, lang)
			}
		}
	}
	if utf8.RuneCountInString(l.Subtitle) > 40 {
		return fmt.Errorf("look %s: subtitle: at most 40 characters", l.ID)
	}
	fallback := compat.Looks(l.AppIconFallback)
	// Installed pre-cutover packages retain their bytes. A retired fallback
	// alias is accepted for reading, but is drawn with the current default.
	if fallback != l.AppIconFallback && slices.Contains(RetiredLookIDs, fallback) {
		fallback = DefaultLook
	}
	if !slices.Contains(BuiltinLooks, fallback) {
		return fmt.Errorf("look %s: app_icon_fallback %q: one of the built-in looks (%s)", l.ID, l.AppIconFallback, strings.Join(BuiltinLooks, ", "))
	}
	if len(l.Avatar.Sizes) == 0 {
		return fmt.Errorf("look %s: avatar.sizes: at least one PNG (the apps that do not draw SVG, the watch, the lock screen)", l.ID)
	}
	if l.Avatar.Safe < 0 || l.Avatar.Safe > 1 {
		return fmt.Errorf("look %s: avatar.safe: 0 to 1", l.ID)
	}
	for _, sizes := range []map[string]string{l.Avatar.Sizes, l.darkSizes()} {
		for side := range sizes {
			n, err := strconv.Atoi(side)
			if err != nil || strconv.Itoa(n) != side || n < LookMinSide || n > LookMaxSide {
				return fmt.Errorf("look %s: avatar size %q: a side in pixels, %d to %d", l.ID, side, LookMinSide, LookMaxSide)
			}
		}
	}
	if l.Colors != nil {
		for scheme, colors := range map[string]map[string]string{"light": l.Colors.Light, "dark": l.Colors.Dark} {
			for role, c := range colors {
				if !slices.Contains(LookColorRoles, role) {
					return fmt.Errorf("look %s: colors.%s.%s: only %s", l.ID, scheme, role, strings.Join(LookColorRoles, ", "))
				}
				if !hexColor.MatchString(c) {
					return fmt.Errorf("look %s: colors.%s.%s %q: #RRGGBB", l.ID, scheme, role, c)
				}
			}
		}
	}
	if l.Dot != nil {
		if _, ok := l.Dot.States["idle"]; !ok {
			return fmt.Errorf("look %s: dot.states: idle is required", l.ID)
		}
		for s := range l.Dot.States {
			if !slices.Contains(LookStates, s) {
				return fmt.Errorf("look %s: dot state %q: only %s", l.ID, s, strings.Join(LookStates, ", "))
			}
		}
		if l.Dot.Size < 0 || l.Dot.Size > LookMaxDotSide {
			return fmt.Errorf("look %s: dot.size: at most %d", l.ID, LookMaxDotSide)
		}
	}
	for _, f := range l.Files() {
		if err := checkPackagePath(f); err != nil {
			return fmt.Errorf("look %s: %v", l.ID, err)
		}
		if strings.HasPrefix(f, ".") || strings.Contains(f, "/.") {
			return fmt.Errorf("look %s: %s: hidden", l.ID, f)
		}
	}
	for _, f := range l.pngs() {
		if !strings.HasSuffix(f, ".png") {
			return fmt.Errorf("look %s: %s: a .png", l.ID, f)
		}
	}
	for _, f := range l.svgs() {
		if !strings.HasSuffix(f, ".svg") {
			return fmt.Errorf("look %s: %s: an .svg", l.ID, f)
		}
	}
	return nil
}

func (l LookItem) darkSizes() map[string]string {
	if l.Avatar.Dark == nil {
		return nil
	}
	return l.Avatar.Dark.Sizes
}

func (l LookItem) svgs() []string {
	var out []string
	if l.Avatar.SVG != "" {
		out = append(out, l.Avatar.SVG)
	}
	if l.Avatar.Dark != nil && l.Avatar.Dark.SVG != "" {
		out = append(out, l.Avatar.Dark.SVG)
	}
	return out
}

func (l LookItem) pngs() []string {
	var out []string
	for _, side := range sortedKeys(l.Avatar.Sizes) {
		out = append(out, l.Avatar.Sizes[side])
	}
	for _, side := range sortedKeys(l.darkSizes()) {
		out = append(out, l.darkSizes()[side])
	}
	if l.Dot != nil {
		for _, s := range sortedKeys(l.Dot.States) {
			out = append(out, l.Dot.States[s].File)
		}
	}
	return out
}

// Files are every file the look names, each once (what the apps download).
func (l LookItem) Files() []string {
	var out []string
	for _, f := range append(l.pngs(), l.svgs()...) {
		if !slices.Contains(out, f) {
			out = append(out, f)
		}
	}
	return out
}

// Looks are the manifest's contributions to looks.
func (m *Manifest) Looks() []LookItem {
	var out []LookItem
	_ = json.Unmarshal(m.Contributes["looks"], &out)
	return out
}

// CheckLookFiles holds a look's files in the package at root to the bounds (wire.CheckLookPNG, wire.CleanSVG): each a
// regular file inside the package (no link out of it), a PNG that decodes, square, within the sides and of the side its
// key says; an SVG the hub can clean; the whole look within LookMaxTotal.
func CheckLookFiles(l LookItem, root string) error {
	var total int64
	files := map[string][]byte{}
	for _, f := range l.Files() {
		b, err := ReadLookFile(root, f)
		if err != nil {
			return fmt.Errorf("look %s: %v", l.ID, err)
		}
		files[f] = b
		total += int64(len(b))
		if strings.HasSuffix(f, ".svg") {
			if _, err := wire.CleanSVG(b); err != nil {
				return fmt.Errorf("look %s: %s: %v", l.ID, f, err)
			}
		}
	}
	if total > LookMaxTotal {
		return fmt.Errorf("look %s: %d bytes, more than %d", l.ID, total, LookMaxTotal)
	}
	for _, sizes := range []map[string]string{l.Avatar.Sizes, l.darkSizes()} {
		for side, f := range sizes {
			n, _ := strconv.Atoi(side)
			if err := wire.CheckLookPNG(files[f], n, LookMaxSide); err != nil {
				return fmt.Errorf("look %s: %s: %v", l.ID, f, err)
			}
		}
	}
	if l.Dot != nil {
		for _, st := range l.Dot.States {
			if err := wire.CheckLookPNG(files[st.File], l.Dot.Size, LookMaxDotSide); err != nil {
				return fmt.Errorf("look %s: %s: %v", l.ID, st.File, err)
			}
		}
	}
	return nil
}

// ReadLookFile reads a file a look names (wire.ReadLookFile): a regular file inside the package within the bounds.
func ReadLookFile(root, name string) ([]byte, error) {
	if err := checkPackagePath(name); err != nil {
		return nil, err
	}
	return wire.ReadLookFile(root, name)
}

// CleanSVG is wire.CleanSVG: the SVG with what could run or reach out taken out.
func CleanSVG(raw []byte) ([]byte, error) { return wire.CleanSVG(raw) }
