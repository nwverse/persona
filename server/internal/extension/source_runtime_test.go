package extension

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/extension/sourcevalidate"
)

// CheckSelfContained is CheckSelfContainedContext without a deadline of the caller's.
func CheckSelfContained(b []byte) error {
	return CheckSelfContainedContext(context.Background(), b)
}

func TestSourceBundleContainmentIntegrityAndSize(t *testing.T) {
	root := t.TempDir()
	b := []byte(`export default {id:"example",version:"1.0.0"}`)
	h := sha256.Sum256(b)
	s := &SourceRuntime{Entry: "src/main.tsx", Bundle: "main.mjs", SHA256: hex.EncodeToString(h[:])}
	if err := os.WriteFile(filepath.Join(root, s.Bundle), b, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckSourceBundle(root, s); err != nil {
		if !errors.Is(err, sourcevalidate.ErrUnavailable) {
			t.Fatal(err)
		}
		t.Log("UNAVAILABLE: independent validator resources; continuing cheap containment/hash/size regressions")
	}
	for _, path := range []string{"../main.mjs", "/main.mjs", "a/../main.mjs", "a\\main.mjs", "./main.mjs"} {
		copy := *s
		copy.Bundle = path
		if _, err := CheckSourceBundle(root, &copy); err == nil {
			t.Errorf("accepted %s", path)
		}
	}
	copy := *s
	copy.SHA256 = strings.Repeat("0", 64)
	if _, err := CheckSourceBundle(root, &copy); err == nil {
		t.Fatal("accepted mismatched digest")
	}
	if err := os.Symlink("main.mjs", filepath.Join(root, "link.mjs")); err != nil {
		t.Fatal(err)
	}
	copy = *s
	copy.Bundle = "link.mjs"
	if _, err := CheckSourceBundle(root, &copy); err == nil {
		t.Fatal("accepted contained symlink")
	}
	f, err := os.OpenFile(filepath.Join(root, "huge.mjs"), os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f.Truncate(SourceBundleMost + 1)
	f.Close()
	copy = *s
	copy.Bundle = "huge.mjs"
	if _, err := CheckSourceBundle(root, &copy); err == nil {
		t.Fatal("accepted oversized artifact")
	}
	for _, source := range []string{`import/*comment*/("https://example.com/code")`, `export {x} from/*comment*/"./x.mjs"`, `import("node:fs")`, "export default `${import('./x.mjs')}`", `const pattern=/"/; import("./x.mjs")`, `export default import.meta`, `let x="./x.mjs"; export default ()=>import(x)`} {
		if err := CheckSelfContained([]byte(source)); err == nil {
			t.Errorf("accepted runtime module %s", source)
		}
	}
}
func TestSourceBundleAllowsHarmlessImportWords(t *testing.T) {
	for _, source := range []string{`export default {text:"Data from a service",imported:true}`, "// import('./x')\nexport default 'import and from'", "export default `text from a service`", `const pattern=/import|from|"/; export default pattern`, `const from=1;export default from`} {
		if err := CheckSelfContained([]byte(source)); err != nil {
			if errors.Is(err, sourcevalidate.ErrUnavailable) {
				t.Skip("UNAVAILABLE: independent production validator resources; module-policy process fixtures are separate")
			}
			t.Fatalf("harmless module rejected: %s: %v", source, err)
		}
	}
	if err := CheckSelfContained([]byte(`export default (`)); err == nil {
		t.Fatal("invalid module syntax accepted")
	}
}

func TestSourceRuntimeManifestMode(t *testing.T) {
	r := &Runtime{Mode: RuntimeDeno, Source: &SourceRuntime{Entry: "src/main.ts", Bundle: "bundle/main.mjs", SHA256: strings.Repeat("0", 64)}}
	if errs := r.validate(); len(errs) != 0 {
		t.Fatal(errs)
	}
	for _, mutate := range []func(*Runtime){func(r *Runtime) { r.Command.Path = "bin/x" }, func(r *Runtime) { r.Env = map[string]string{"X": "y"} }, func(r *Runtime) { r.RPC = "none" }, func(r *Runtime) { r.HTTP = true }, func(r *Runtime) { r.Mode = "process" }, func(r *Runtime) { r.Source = nil }} {
		copy := *r
		mutate(&copy)
		if len(copy.validate()) == 0 {
			t.Fatal("accepted incompatible source runtime")
		}
	}
}
