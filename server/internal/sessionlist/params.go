package sessionlist

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Enabled distinguishes the indexed API from legacy source/provider/q and
// machine_id filtering. Presence matters: malformed new parameters must be
// parsed and rejected rather than silently falling back to the legacy API.
func Enabled(v url.Values) bool {
	for _, key := range []string{"limit", "cursor", "since", "summary", "state", "project", "group", "counts", "pane_source"} {
		if _, exists := v[key]; exists {
			return true
		}
	}
	return false
}

var runtimeKeys = [...]string{"facets", "project_id", "harness", "group_by", "sort", "direction", "agents_only", "scope"}

func RuntimeEnabled(v url.Values) bool { return runtimeDimensions(v) || Enabled(v) }

func runtimeDimensions(v url.Values) bool {
	if len(v["machine_id"]) > 1 {
		return true
	}
	for _, k := range runtimeKeys {
		if _, present := v[k]; present {
			return true
		}
	}
	return false
}

// ParseRuntime leaves personal-agent adapters on Parse's existing contract.
func ParseRuntime(v url.Values) (Options, error) {
	if !runtimeDimensions(v) {
		return Parse(v)
	}
	legacy := make(url.Values, len(v))
	for k, values := range v {
		if k != "machine_id" {
			legacy[k] = values
		}
	}
	o, err := Parse(legacy)
	if err != nil {
		return Options{}, err
	}
	for _, k := range runtimeKeys {
		if values, present := v[k]; present && k != "project_id" && k != "harness" && (len(values) != 1 || values[0] == "") {
			return Options{}, failure("invalid_query", k+" requires exactly one nonempty value")
		}
	}
	for k, dst := range map[string]*bool{"facets": &o.Facets, "agents_only": &o.AgentsOnly} {
		if v.Has(k) {
			if *dst, err = boolean(v.Get(k)); err != nil {
				return Options{}, err
			}
		}
	}
	for k, dst := range map[string]*string{"machine_id": &o.machines, "project_id": &o.projects, "harness": &o.harnesses} {
		if values, present := v[k]; present {
			if len(values) == 0 || len(values) > maxLimit {
				return Options{}, failure("invalid_query", k+" has an invalid number of values")
			}
			for _, value := range values {
				if value == "" || strings.ContainsRune(value, 0) || !utf8.ValidString(value) || utf8.RuneCountInString(value) > 256 {
					return Options{}, failure("invalid_query", k+" requires nonempty identifiers of at most 256 Unicode characters")
				}
			}
			*dst = valueSet(values)
		}
	}
	if v.Has("scope") {
		if v.Get("scope") != "work" {
			return Options{}, failure("invalid_query", "scope must be work")
		}
		o.WorkOnly = true
	}
	o.Sort, o.Direction, o.GroupBy = v.Get("sort"), v.Get("direction"), v.Get("group_by")
	return validate(o)
}

// UsesDimensions tells runtime adapters to authorize before slicing the view.
func (o Options) UsesDimensions() bool {
	return o.Facets || o.AgentsOnly || o.WorkOnly || o.Sort != "" || o.Direction != "" || o.GroupBy != "" ||
		o.machines != "" || o.projects != "" || o.harnesses != ""
}

// RestrictMachines binds a runtime view to its current authorization scope. An
// empty scope matches no records; the zero (unset) scope matches all machines.
func (o *Options) RestrictMachines(ids []string) { o.scope = valueSet(ids) }

func valueSet(values []string) string {
	ids := slices.Clone(values)
	ids = slices.DeleteFunc(ids, func(id string) bool { return id == "" || strings.ContainsRune(id, 0) })
	slices.Sort(ids)
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return "\x00"
	}
	return "\x00" + strings.Join(ids, "\x00") + "\x00"
}

func setMatches(set, value string) bool {
	return set == "" || (!strings.ContainsRune(value, 0) && strings.Contains(set, "\x00"+value+"\x00"))
}

// Parse strictly validates recognized parameters. Unknown parameters remain
// available to adapters. The counts selector is validated but is not an Options
// field: adapters choose the counts endpoint, while Query ignores the selector.
func Parse(v url.Values) (Options, error) {
	o := Options{Limit: defaultLimit}
	for _, key := range []string{"limit", "cursor", "since", "summary", "state", "machine_id", "project", "group", "source", "provider", "q", "counts", "pane_source"} {
		if values, exists := v[key]; exists && len(values) != 1 {
			return Options{}, failure("invalid_query", key+" must have exactly one value")
		}
	}
	if values, exists := v["limit"]; exists {
		n, err := positiveInteger(values[0])
		if err != nil || n > maxLimit {
			return Options{}, failure("invalid_query", "limit must be between 1 and 200")
		}
		o.Limit = n
	}
	if values, exists := v["summary"]; exists {
		b, err := boolean(values[0])
		if err != nil {
			return Options{}, err
		}
		o.Summary = b
	}
	if values, exists := v["counts"]; exists {
		if _, err := boolean(values[0]); err != nil {
			return Options{}, err
		}
	}
	o.Cursor, o.Since = v.Get("cursor"), v.Get("since")
	if _, exists := v["cursor"]; exists && o.Cursor == "" {
		return Options{}, failure("invalid_query", "cursor must not be empty")
	}
	if _, exists := v["since"]; exists && o.Since == "" {
		return Options{}, failure("invalid_query", "since must not be empty")
	}
	o.State, o.Machine = v.Get("state"), v.Get("machine_id")
	o.Project, o.Group = v.Get("project"), v.Get("group")
	o.Source, o.Provider, o.Q = v.Get("source"), v.Get("provider"), v.Get("q")
	if values, exists := v["pane_source"]; exists {
		if values[0] == "" {
			return Options{}, failure("invalid_query", "pane_source must not be empty")
		}
		o.PaneSource = values[0]
	}
	return validate(o)
}

func boolean(s string) (bool, error) {
	switch s {
	case "true", "1":
		return true, nil
	case "false", "0":
		return false, nil
	default:
		return false, failure("invalid_query", "boolean parameters must be true, false, 1, or 0")
	}
}

func positiveInteger(s string) (int, error) {
	n, err := counter(s)
	if err != nil || n == 0 || n > maxLimit {
		return 0, failure("invalid_query", "invalid positive integer")
	}
	return int(n), nil
}

func validate(o Options) (Options, error) {
	if o.WorkOnly && o.AgentsOnly {
		return Options{}, failure("invalid_query", "scope=work cannot be combined with agents_only=1")
	}
	if o.ID != "" && (o.Cursor != "" || o.Since != "") {
		return Options{}, failure("invalid_query", "exact identity cannot be combined with cursor or since")
	}
	if o.Limit == 0 {
		o.Limit = defaultLimit
	}
	if o.Limit < 1 || o.Limit > maxLimit {
		return Options{}, failure("invalid_query", "limit must be between 1 and 200")
	}
	switch o.PaneSource {
	case "", "terminal", "feishu", "desktop", "unknown":
	default:
		return Options{}, failure("invalid_query", "pane_source is not a supported pane origin")
	}
	switch o.State {
	case "", "needs_you", "working", "unread", "idle", "offline", "unknown":
	default:
		return Options{}, failure("invalid_query", "state is not a supported agent state")
	}
	if !utf8.ValidString(o.Q) || utf8.RuneCountInString(o.Q) > 256 {
		return Options{}, failure("invalid_query", "q must contain at most 256 Unicode characters")
	}
	if o.Since != "" {
		if _, _, err := parseRevision(o.Since); err != nil {
			return Options{}, err
		}
	}
	if o.Cursor != "" {
		if _, err := parseCursor(o.Cursor); err != nil {
			return Options{}, err
		}
	}
	if _, err := groupDimensions(o.GroupBy); err != nil {
		return Options{}, err
	}
	switch o.Sort {
	case "", "recent", "created", "title":
	default:
		return Options{}, failure("invalid_query", "sort is not supported")
	}
	switch o.Direction {
	case "", "asc", "desc":
	default:
		return Options{}, failure("invalid_query", "direction is not supported")
	}
	if o.Sort == "" && o.Direction != "" {
		o.Sort = "recent"
	}
	if o.Sort != "" && o.Direction == "" {
		o.Direction = "desc"
		if o.Sort == "title" {
			o.Direction = "asc"
		}
	}
	o.Q = normalizeQ(o.Q)
	return o, nil
}

func normalizeQ(s string) string { return strings.ToLower(s) }

func (o Options) filters() filters {
	return filters{ID: o.ID, State: o.State, Machine: o.Machine, Project: o.Project, Group: o.Group, Source: o.Source, Provider: o.Provider, Q: o.Q, PaneSource: o.PaneSource, Manager: o.Manager,
		machines: o.machines, projects: o.projects, harnesses: o.harnesses, scope: o.scope, agentsOnly: o.AgentsOnly, workOnly: o.WorkOnly}
}

func (o Options) binding() binding {
	return binding{filters: o.filters(), Summary: o.Summary, Since: o.Since, viewOptions: o.view()}
}

func validTokenPart(s string) bool {
	if len(s) != 22 {
		return false
	}
	b, err := base64.RawURLEncoding.DecodeString(s)
	return err == nil && len(b) == 16 && base64.RawURLEncoding.EncodeToString(b) == s
}

func counter(s string) (uint64, error) {
	if s == "" || (len(s) > 1 && s[0] == '0') {
		return 0, failure("invalid_query", "malformed token counter")
	}
	for _, ch := range s {
		if ch < '0' || ch > '9' {
			return 0, failure("invalid_query", "malformed token counter")
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil {
		return 0, failure("invalid_query", "malformed token counter")
	}
	return n, nil
}

func parseRevision(s string) (string, uint64, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 3 || parts[0] != "v1" || !validTokenPart(parts[1]) {
		return "", 0, failure("invalid_query", "malformed revision token")
	}
	n, err := counter(parts[2])
	return parts[1], n, err
}

func (i *Index) revisionToken() string {
	return "v1." + i.epoch + "." + strconv.FormatUint(i.revision, 10)
}

func (i *Index) sinceRevision(token string) (uint64, error) {
	epoch, revision, err := parseRevision(token)
	if err != nil {
		return 0, err
	}
	if epoch != i.epoch {
		return 0, failure("revision_expired", "revision belongs to another index epoch")
	}
	if revision > i.revision {
		return 0, failure("invalid_query", "revision is ahead of the index")
	}
	if revision < i.floor {
		return 0, failure("revision_expired", "revision is no longer in the change journal")
	}
	return revision, nil
}

type cursorParts struct {
	epoch, id, signed, signature string
	offset                       int
}

func parseCursor(s string) (cursorParts, error) {
	parts := strings.Split(s, ".")
	if len(parts) != 5 || parts[0] != "c1" || !validTokenPart(parts[1]) || !validTokenPart(parts[2]) || !validTokenPart(parts[4]) {
		return cursorParts{}, failure("invalid_query", "malformed cursor token")
	}
	n, err := counter(parts[3])
	// Offsets must fit int on all supported architectures.
	if err != nil || n == 0 || n > uint64(^uint(0)>>1) {
		return cursorParts{}, failure("invalid_query", "malformed cursor offset")
	}
	return cursorParts{epoch: parts[1], id: parts[2], signed: strings.Join(parts[:4], "."), signature: parts[4], offset: int(n)}, nil
}

func (i *Index) signature(s string) string {
	mac := hmac.New(sha256.New, i.secret)
	mac.Write([]byte(s))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil)[:16])
}

func (i *Index) cursorToken(id string, offset int) string {
	s := "c1." + i.epoch + "." + id + "." + strconv.Itoa(offset)
	return s + "." + i.signature(s)
}

func (i *Index) cursorSnapshot(o Options) (*snapshot, string, int, error) {
	parts, err := parseCursor(o.Cursor)
	if err != nil {
		return nil, "", 0, err
	}
	if parts.epoch != i.epoch {
		return nil, "", 0, failure("cursor_expired", "cursor belongs to another index epoch")
	}
	if !hmac.Equal([]byte(parts.signature), []byte(i.signature(parts.signed))) {
		return nil, "", 0, failure("invalid_query", "invalid cursor signature")
	}
	s := i.snapshots[parts.id]
	if s == nil {
		return nil, "", 0, failure("cursor_expired", "cursor snapshot expired or was evicted")
	}
	if s.binding != o.binding() {
		return nil, "", 0, failure("invalid_query", "cursor does not match the query")
	}
	if parts.offset >= len(s.result.events) {
		return nil, "", 0, failure("invalid_query", "cursor offset is outside its snapshot")
	}
	return s, parts.id, parts.offset, nil
}
