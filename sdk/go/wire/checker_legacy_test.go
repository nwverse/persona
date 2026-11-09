package wire

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

// legacyChecker is (*checker).check as it was before it built locations only
// on error (#2718), kept verbatim as the oracle: every value must get the same
// errors, in the same order, from the current checker.
type legacyChecker struct{ errs []string }

func (c *legacyChecker) add(format string, a ...any) {
	c.errs = append(c.errs, fmt.Sprintf(format, a...))
}

func (c *legacyChecker) full() bool { return len(c.errs) >= errorsMost }

func (c *legacyChecker) check(s map[string]any, v any, here, at string) {
	if c.full() || s == nil {
		return
	}
	if ref, ok := s["$ref"].(string); ok {
		target, f := resolve(ref, here)
		if target == nil {
			c.add("%s: the schema has no %s", at, ref)
			return
		}
		c.check(target, v, f, at)
		rest := map[string]any{}
		for k, x := range s {
			if k != "$ref" && k != "description" {
				rest[k] = x
			}
		}
		if len(rest) > 0 {
			c.check(rest, v, here, at)
		}
		return
	}
	if all, ok := s["allOf"].([]any); ok {
		for _, option := range all {
			c.check(option.(map[string]any), v, here, at)
		}
	}
	if anyOf, ok := s["anyOf"].([]any); ok {
		passing := false
		for _, option := range anyOf {
			sub := legacyChecker{}
			sub.check(option.(map[string]any), v, here, at)
			if len(sub.errs) == 0 {
				passing = true
				break
			}
		}
		if !passing {
			c.add("%s: no matching anyOf branch", at)
		}
	}
	if forbidden, ok := s["not"].(map[string]any); ok {
		sub := legacyChecker{}
		sub.check(forbidden, v, here, at)
		if len(sub.errs) == 0 {
			c.add("%s: matches forbidden schema", at)
		}
	}
	if one, ok := s["oneOf"].([]any); ok {
		if disc, ok := s["x-discriminator"].(string); ok {
			obj, _ := v.(map[string]any)
			tag := obj[disc]
			matched := false
			for _, o := range one {
				option := o.(map[string]any)
				ref, _ := option["$ref"].(string)
				target, _ := resolve(ref, here)
				props, _ := target["properties"].(map[string]any)
				d, _ := props[disc].(map[string]any)
				if tag != nil && d["const"] == tag {
					// Check the complete branch, including overlays next to its $ref.
					c.check(option, v, here, at)
					matched = true
					break
				}
			}
			if !matched {
				c.add("%s: no variant with %s %s", at, disc, show(tag))
				return
			}
		} else {
			passing := 0
			for _, o := range one {
				sub := legacyChecker{}
				sub.check(o.(map[string]any), v, here, at)
				if len(sub.errs) == 0 {
					passing++
				}
			}
			if passing != 1 {
				c.add("%s: matches %d of oneOf, not one", at, passing)
			}
		}
		// All oneOf forms also retain their sibling type/properties constraints.
	}
	if t, ok := s["type"].(string); ok && !isType(t, v) {
		c.add("%s: not of type %s", at, t)
		return
	}
	if want, ok := s["const"]; ok && !equal(v, want) {
		c.add("%s: not %s", at, show(want))
	}
	if enum, ok := s["enum"].([]any); ok {
		in := false
		for _, e := range enum {
			if equal(v, e) {
				in = true
			}
		}
		if !in {
			c.add("%s: %s is not one of %s", at, show(v), show(enum))
		}
	}
	switch x := v.(type) {
	case string:
		n := utf8.RuneCountInString(x)
		if m, ok := num(s["minLength"]); ok && float64(n) < m {
			c.add("%s: shorter than %v", at, m)
		}
		if m, ok := num(s["maxLength"]); ok && float64(n) > m {
			c.add("%s: longer than %v", at, m)
		}
		if p, ok := s["pattern"].(string); ok {
			if re := pattern(p); re == nil || !re.MatchString(x) {
				c.add("%s: %s does not match %s", at, show(x), p)
			}
		}
	case float64:
		if m, ok := num(s["minimum"]); ok && x < m {
			c.add("%s: below %v", at, m)
		}
		if m, ok := num(s["maximum"]); ok && x > m {
			c.add("%s: above %v", at, m)
		}
	case json.Number:
		if n, ok := preciseNumber(x); ok {
			if m, ok := preciseNumber(s["minimum"]); ok && n.Cmp(m) < 0 {
				c.add("%s: below %s", at, show(s["minimum"]))
			}
			if m, ok := preciseNumber(s["maximum"]); ok && n.Cmp(m) > 0 {
				c.add("%s: above %s", at, show(s["maximum"]))
			}
		}
	case []any:
		if m, ok := num(s["minItems"]); ok && float64(len(x)) < m {
			c.add("%s: fewer than %v items", at, m)
		}
		if m, ok := num(s["maxItems"]); ok && float64(len(x)) > m {
			c.add("%s: more than %v items", at, m)
		}
		if s["uniqueItems"] == true {
			seen := map[string]bool{}
			for i, e := range x {
				if k := show(e); seen[k] {
					c.add("%s[%d]: %s twice", at, i, k)
				} else {
					seen[k] = true
				}
			}
		}
		items, _ := s["items"].(map[string]any)
		for i, e := range x {
			c.check(items, e, here, fmt.Sprintf("%s[%d]", at, i))
		}
	case map[string]any:
		props, _ := s["properties"].(map[string]any)
		if req, ok := s["required"].([]any); ok {
			for _, k := range req {
				if _, has := x[k.(string)]; !has {
					c.add("%s: no %s", at, k)
				}
			}
		}
		if m, ok := num(s["minProperties"]); ok && float64(len(x)) < m {
			c.add("%s: fewer than %v properties", at, m)
		}
		if m, ok := num(s["maxProperties"]); ok && float64(len(x)) > m {
			c.add("%s: more than %v properties", at, m)
		}
		extra := s["additionalProperties"]
		for _, k := range sortedKeys(x) {
			if names, ok := s["propertyNames"].(map[string]any); ok {
				c.check(names, k, here, at+".<key>")
			}
			if p, ok := props[k].(map[string]any); ok {
				c.check(p, x[k], here, at+"."+k)
			} else if extra == false {
				c.add("%s: unknown field %s", at, k)
			} else if e, ok := extra.(map[string]any); ok {
				c.check(e, x[k], here, at+"."+k)
			}
		}
	}
}

// legacyErrors is Validate's lookup of def with the legacy checker.
func legacyErrors(t *testing.T, def string, v any) []string {
	t.Helper()
	v = compatibilityValue(def, v)
	for name, doc := range files {
		if s, ok := defs(doc)[def].(map[string]any); ok {
			c := legacyChecker{}
			c.check(s, v, name, "$")
			return c.errs
		}
	}
	return nil
}

func currentErrors(def string, v any) []string {
	var ve *ValidationError
	if err := Validate(def, v); err != nil {
		ve, _ = err.(*ValidationError)
		if ve == nil {
			return []string{"error: " + err.Error()}
		}
		return ve.Errors
	}
	return nil
}

// mutations returns copies of v with one change each, at every depth: a member
// dropped, a member added, a value replaced with each JSON kind, an array item
// repeated. Limited so a large example does not explode.
func mutations(v any, rng *rand.Rand, limit int) []any {
	var out []any
	var walk func(node any, set func(any) any)
	walk = func(node any, set func(any) any) {
		if len(out) >= limit {
			return
		}
		for _, replacement := range []any{nil, "x", 1.5, -1.0, true, []any{}, map[string]any{}, strings.Repeat("y", 300)} {
			out = append(out, set(replacement))
		}
		switch x := node.(type) {
		case map[string]any:
			keys := sortedKeys(x)
			out = append(out, set(withMember(x, "unexpected_member", "z")))
			for _, k := range keys {
				out = append(out, set(withoutMember(x, k)))
				k := k
				walk(x[k], func(r any) any { return set(withMember(x, k, r)) })
			}
		case []any:
			if len(x) > 0 {
				out = append(out, set(append(slices.Clone(x), x[0])))
			}
			for i := range x {
				i := i
				if rng.IntN(3) == 0 || len(x) < 4 {
					walk(x[i], func(r any) any { c := slices.Clone(x); c[i] = r; return set(c) })
				}
			}
		}
	}
	walk(v, func(r any) any { return r })
	return out
}

func withMember(m map[string]any, k string, v any) map[string]any {
	c := make(map[string]any, len(m)+1)
	for key, value := range m {
		c[key] = value
	}
	c[k] = v
	return c
}

func withoutMember(m map[string]any, k string) map[string]any {
	c := make(map[string]any, len(m))
	for key, value := range m {
		if key != k {
			c[key] = value
		}
	}
	return c
}

// The checker reports exactly what the legacy one did, byte for byte and in
// order, for every schema example, valid and invalid, and for one-change
// mutations of each at every depth, and for page-sized views.
func TestCheckerMatchesLegacy(t *testing.T) {
	loadOnce.Do(loadSchemas)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	var paths []string
	root := filepath.Join("..", "..", "..", "schema", "extension", "examples")
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".json") {
			paths = append(paths, p)
		}
		return nil
	})
	rng := rand.New(rand.NewPCG(1, 2))
	compared, failing := 0, 0
	compare := func(name, def string, v any) {
		t.Helper()
		want := legacyErrors(t, def, v)
		got := currentErrors(def, v)
		if !slices.Equal(want, got) {
			t.Fatalf("%s (%s):\nlegacy  %q\ncurrent %q", name, def, want, got)
		}
		compared++
		if len(want) > 0 {
			failing++
		}
	}
	for _, p := range paths {
		def := strings.SplitN(filepath.Base(p), ".", 2)[0]
		if !hasDef(def) {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if json.Unmarshal(raw, &v) != nil || !utf8.Valid(raw) {
			continue
		}
		compare(p, def, v)
		for i, m := range mutations(v, rng, 400) {
			compare(fmt.Sprintf("%s mutation %d", p, i), def, m)
		}
	}
	for _, items := range []int{8, 64} {
		raw, err := json.Marshal(benchView(items))
		if err != nil {
			t.Fatal(err)
		}
		var v any
		if err := json.Unmarshal(raw, &v); err != nil {
			t.Fatal(err)
		}
		for i, m := range append([]any{v}, mutations(v, rng, 600)...) {
			compare(fmt.Sprintf("benchView(%d) mutation %d", items, i), "View", m)
		}
	}
	if compared < 5000 || failing < compared/4 {
		t.Fatalf("compared %d values, %d of them failing: too few to tell the checkers apart", compared, failing)
	}
	t.Logf("compared %d values, %d failing", compared, failing)
}

func hasDef(def string) bool {
	for _, doc := range files {
		if _, ok := defs(doc)[def].(map[string]any); ok {
			return true
		}
	}
	return false
}
