package extension

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nwverse/persona/server/internal/extension/sourcevalidate"
	"github.com/nwverse/persona/server/internal/i18n"
)

const RuntimeDeno = "deno"
const SourceBundleMost = 16 << 20

// SourceRuntime names the authoring entry and the signed, self-contained JavaScript artifact.
// Entry is used only by the offline build command, never by activation.
type SourceRuntime struct {
	Entry  string `json:"entry"`
	Bundle string `json:"bundle"`
	SHA256 string `json:"sha256"`
}

func (r *Runtime) IsDeno() bool { return r != nil && r.Mode == RuntimeDeno }

func (r *Runtime) validateSource() []error {
	var errs []error
	bad := func(s string) { errs = append(errs, errors.New(s)) }
	if r.Source == nil {
		return []error{errors.New("runtime.source: required for deno")}
	}
	if r.Command.Path != "" || r.Command.ByPlatform != nil || len(r.Args) != 0 || len(r.Env) != 0 || r.HTTP || !r.SpeaksRPC() {
		bad("runtime deno: command, args, env, http and rpc none are not supported")
	}
	for _, p := range []string{r.Source.Entry, r.Source.Bundle} {
		if !sourcePath(p) {
			bad("runtime.source: paths must be contained, normalized relative paths")
		}
	}
	if !(strings.HasSuffix(r.Source.Entry, ".ts") || strings.HasSuffix(r.Source.Entry, ".tsx")) {
		bad("runtime.source.entry: expected .ts or .tsx")
	}
	if !strings.HasSuffix(r.Source.Bundle, ".mjs") {
		bad("runtime.source.bundle: expected .mjs")
	}
	if !ValidSHA256(r.Source.SHA256) {
		bad("runtime.source.sha256: expected lowercase SHA-256")
	}
	return errs
}

func ValidSHA256(s string) bool {
	b, err := hex.DecodeString(s)
	return err == nil && len(b) == sha256.Size && s == strings.ToLower(s)
}

func sourcePath(p string) bool {
	return p != "" && filepath.IsLocal(p) && filepath.ToSlash(filepath.Clean(p)) == p && !strings.ContainsAny(p, "\\\x00\r\n") && p != "."
}

// SourceFile rejects links at every component, even links that would resolve inside the package.
func SourceFile(root, rel string) (string, error) {
	if !sourcePath(rel) {
		return "", errors.New("source path is not contained")
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	p := root
	parts := strings.Split(rel, "/")
	for i, part := range parts {
		p = filepath.Join(p, part)
		st, err := os.Lstat(p)
		if err != nil {
			return "", err
		}
		if st.Mode()&os.ModeSymlink != 0 || (i < len(parts)-1 && !st.IsDir()) || (i == len(parts)-1 && !st.Mode().IsRegular()) {
			return "", errors.New("source file must be regular and must not traverse symlinks")
		}
	}
	return p, nil
}

// CheckSourceBundle is used at package validation and again immediately before every activation.
func CheckSourceBundle(root string, s *SourceRuntime) (string, error) {
	return CheckSourceBundleContext(context.Background(), root, s)
}

// CheckSourceBundleContext performs only bounded filesystem and hash operations
// in the manager. The independent child alone constructs and traverses the AST.
func CheckSourceBundleContext(ctx context.Context, root string, s *SourceRuntime) (string, error) {
	f, p, err := openSourceBundle(root, s)
	if err != nil {
		return "", err
	}
	defer f.Close()
	if err := sourcevalidate.Validate(ctx, f, s.SHA256); err != nil {
		return "", err
	}
	return p, nil
}

// SnapshotSourceBundleContext returns a sealed artifact descriptor, not a
// mutable package pathname. The caller owns it through process exit.
func SnapshotSourceBundleContext(ctx context.Context, root string, s *SourceRuntime) (*os.File, error) {
	f, err := snapshotSourceBundle(root, s)
	if err != nil {
		return nil, err
	}
	if err := sourcevalidate.Validate(ctx, f, s.SHA256); err != nil {
		_ = f.Close()
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		_ = f.Close()
		return nil, sourcevalidate.ErrUnavailable
	}
	return f, nil
}

func snapshotSourceBundle(root string, s *SourceRuntime) (*os.File, error) {
	input, _, err := openSourceBundle(root, s)
	if err != nil {
		return nil, err
	}
	defer input.Close()
	f, err := newSourceSnapshot()
	if err != nil {
		return nil, sourcevalidate.ErrUnavailable
	}
	fail := func(err error) (*os.File, error) { _ = f.Close(); return nil, err }
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(input, SourceBundleMost+1))
	if err != nil || n == 0 || n > SourceBundleMost || hex.EncodeToString(h.Sum(nil)) != s.SHA256 {
		return fail(i18n.ErrEnglish("extension-source.validator_inaccessible"))
	}
	if err := sealSourceSnapshot(f); err != nil {
		return fail(sourcevalidate.ErrUnavailable)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(i18n.ErrEnglish("extension-source.validator_inaccessible"))
	}
	return f, nil
}

// CheckSourceBundleIntegrity is the cheap commit-time recheck after the same
// signed digest passed preview validation. It does not grant syntax acceptance
// or runtime activation; those always require CheckSourceBundleContext.
func CheckSourceBundleIntegrity(root string, s *SourceRuntime) (string, error) {
	f, p, err := openSourceBundle(root, s)
	if err != nil {
		return "", err
	}
	defer f.Close()
	return p, nil
}

func openSourceBundle(root string, s *SourceRuntime) (*os.File, string, error) {
	if s == nil || !ValidSHA256(s.SHA256) {
		return nil, "", errors.New("source bundle integrity is required")
	}
	p, err := SourceFile(root, s.Bundle)
	if err != nil {
		return nil, "", i18n.ErrEnglish("extension-source.validator_inaccessible")
	}
	f, err := openSourceFile(root, s.Bundle)
	if err != nil {
		if errors.Is(err, sourcevalidate.ErrUnavailable) {
			return nil, "", err
		}
		return nil, "", i18n.ErrEnglish("extension-source.validator_inaccessible")
	}
	fail := func(err error) (*os.File, string, error) { _ = f.Close(); return nil, "", err }
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Size() == 0 || st.Size() > SourceBundleMost {
		return fail(errors.New("source bundle must be nonempty and at most 16 MiB"))
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, SourceBundleMost+1))
	if err != nil || n == 0 || n > SourceBundleMost {
		return fail(errors.New("source bundle exceeds its size limit"))
	}
	if hex.EncodeToString(h.Sum(nil)) != s.SHA256 {
		return fail(errors.New("source bundle integrity mismatch"))
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fail(i18n.ErrEnglish("extension-source.validator_inaccessible"))
	}
	return f, p, nil
}

// CheckSelfContainedContext never parses source in the caller: it sends bounded bytes
// to the same resource-bounded validator used for installation and activation.
func CheckSelfContainedContext(ctx context.Context, b []byte) error {
	if len(b) == 0 || len(b) > SourceBundleMost {
		return errors.New("JavaScript artifact exceeds its size budget")
	}
	h := sha256.Sum256(b)
	return sourcevalidate.Validate(ctx, bytes.NewReader(b), hex.EncodeToString(h[:]))
}
