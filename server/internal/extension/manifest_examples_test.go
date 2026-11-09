package extension

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/compat"
)

// The JSON Schema only checks shape; Parse also runs the hub's semantic Validate. The example manifests are what
// the Go / TS / Swift / Kotlin round-trip tests hold up as valid, so every one of them must be a manifest the hub
// installs, and every invalid one must be refused.
func TestManifestExamplesParse(t *testing.T) {
	const dir = "../../../schema/extension/examples"
	// Shape-only fixtures: they exercise the generated types, not what the hub installs.
	shapeOnly := map[string]string{
		// The custom-contribution round-trip fixture (#2577): odd keys and no runtime on purpose.
		"Manifest.custom-contributions.json": "contributes.ui needs a runtime",
	}
	valid := globExamples(t, filepath.Join(dir, "Manifest.*.json"))
	for name := range shapeOnly {
		if !slices.Contains(valid, filepath.Join(dir, name)) {
			t.Errorf("shape-only fixture %s is gone; drop it from the list", name)
		}
	}
	for _, path := range valid {
		t.Run(filepath.Base(path), func(t *testing.T) {
			if why, ok := shapeOnly[filepath.Base(path)]; ok {
				if _, err := Parse(readExample(t, path)); err == nil || !strings.Contains(err.Error(), why) {
					t.Fatalf("shape-only fixture: want an error with %q, got %v; if the hub installs it now, drop it from the list", why, err)
				}
				return
			}
			if _, err := Parse(readExample(t, path)); err != nil {
				t.Fatalf("the hub refuses a valid example: %v", err)
			}
		})
	}
	invalid := globExamples(t, filepath.Join(dir, "invalid", "Manifest.*.json"))
	for _, path := range invalid {
		t.Run("invalid/"+filepath.Base(path), func(t *testing.T) {
			if _, err := Parse(readExample(t, path)); err == nil {
				t.Fatal("the hub accepts an invalid example")
			}
		})
	}
}

// The SDK templates are what `personad extension new` writes out; with the placeholders filled in they must install.
func TestManifestExamplesTemplatesParse(t *testing.T) {
	fill := strings.NewReplacer("{{id}}", "example-template", "{{name}}", "示例模板")
	for _, lang := range []string{"go", "ts"} {
		t.Run(lang, func(t *testing.T) {
			path := filepath.Join("../../../sdk/templates", lang, "persona-extension.json")
			data := fill.Replace(string(readExample(t, path)))
			if strings.Contains(data, "{{") {
				t.Fatalf("%s has a placeholder this test does not fill", path)
			}
			if _, err := Parse([]byte(data)); err != nil {
				t.Fatalf("the hub refuses the %s template: %v", lang, err)
			}
		})
	}
}

// The manifest schema refuses the connector cli names the hub reserves (reservedCommands), so that a catalog's and
// the SDK's validation fail on them as the install does. The legacy daemon name aside: legacy literals stay in compat.
func TestManifestSchemaReservesTheHubsCommands(t *testing.T) {
	var schema struct {
		Defs map[string]struct {
			Properties map[string]struct {
				Not struct{ Enum []string }
			}
		} `json:"$defs"`
	}
	if err := json.Unmarshal(readExample(t, "../../../schema/extension/manifest.schema.json"), &schema); err != nil {
		t.Fatal(err)
	}
	got := slices.Sorted(slices.Values(schema.Defs["ConnectorItem"].Properties["cli"].Not.Enum))
	want := slices.Sorted(slices.Values(slices.DeleteFunc(slices.Clone(reservedCommands), func(c string) bool { return c == compat.DaemonBinary })))
	if !slices.Equal(got, want) {
		t.Fatalf("manifest.schema.json ConnectorItem.cli refuses %v, the hub reserves %v", got, want)
	}
}

func globExamples(t *testing.T, pattern string) []string {
	t.Helper()
	paths, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatalf("no manifests match %s", pattern)
	}
	return paths
}

func readExample(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
