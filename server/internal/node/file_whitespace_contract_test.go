package node

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func whitespaceRPC(t *testing.T, f *uploadRPCFixture, method string, params map[string]any, out any) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return f.peer.Call(ctx, method, params, out)
}

func isolatedWhitespaceFixture(t *testing.T) *uploadRPCFixture {
	t.Helper()
	f := newUploadRPCFixture(t)
	t.Setenv("HOME", f.n.home)
	t.Setenv("PERSONA_HOME", filepath.Join(f.n.home, ".persona"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(f.n.home, "xdg"))
	return f
}

func whitespaceHome(t *testing.T) (*uploadRPCFixture, string) {
	t.Helper()
	f := isolatedWhitespaceFixture(t)
	dir := filepath.Join(f.n.home, "project")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	return f, dir
}

func assertWhitespaceText(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Fatalf("%q: got %q, %v; want %q", path, got, err, want)
	}
}

func TestWhitespacePathConsumersRPC(t *testing.T) {
	for _, suffix := range []string{" ", "\u00a0"} {
		t.Run(fmtWhitespace(suffix), func(t *testing.T) {
			if runtime.GOOS == "windows" && suffix == " " {
				t.Skip("Windows does not safely resolve trailing ASCII spaces")
			}
			f, dir := whitespaceHome(t)
			plain, selected := filepath.Join(dir, "read"), filepath.Join(dir, "read"+suffix)
			makeFile(t, plain, "plain")
			makeFile(t, selected, "selected")
			a, ae := os.Stat(plain)
			b, be := os.Stat(selected)
			if ae != nil || be != nil || os.SameFile(a, b) {
				t.Skip("filesystem does not distinguish these names")
			}
			for _, name := range []string{"dest", "dest" + suffix} {
				if err := os.Mkdir(filepath.Join(dir, name), 0o700); err != nil {
					t.Fatal(err)
				}
			}
			makeFile(t, filepath.Join(dir, "dest", "marker"), "plain directory")
			makeFile(t, filepath.Join(dir, "dest"+suffix, "marker"), "selected directory")
			makeFile(t, filepath.Join(dir, "dest", "upload"), "untouched")
			makeFile(t, filepath.Join(dir, "dest"+suffix, "upload"), "untouched in selected directory")
			makeFile(t, filepath.Join(dir, "dest"+suffix, "only-selected"), "marker")

			var listing struct {
				Entries []struct {
					Name string `json:"name"`
				} `json:"entries"`
			}
			if err := whitespaceRPC(t, f, "fs.list", map[string]any{"path": "~/project"}, &listing); err != nil {
				t.Fatal(err)
			}
			found := map[string]bool{}
			for _, item := range listing.Entries {
				found[item.Name] = true
			}
			if !found["read"] || !found["read"+suffix] {
				t.Fatalf("listing lost distinct names: %+v", listing)
			}
			listing.Entries = nil
			if err := whitespaceRPC(t, f, "fs.list", map[string]any{"path": "~/project/dest" + suffix}, &listing); err != nil {
				t.Fatal(err)
			}
			found = map[string]bool{}
			for _, item := range listing.Entries {
				found[item.Name] = true
			}
			if !found["only-selected"] {
				t.Fatalf("listed a different directory: %+v", listing)
			}
			var foundFiles struct {
				Files []string `json:"files"`
			}
			if err := whitespaceRPC(t, f, "fs.find", map[string]any{"cwd": "~/project/dest" + suffix}, &foundFiles); err != nil {
				t.Fatal(err)
			}
			found = map[string]bool{}
			for _, name := range foundFiles.Files {
				found[name] = true
			}
			if !found["only-selected"] {
				t.Fatalf("searched a different directory: %+v", foundFiles)
			}
			var workspace struct {
				Cwd string `json:"cwd"`
			}
			if err := whitespaceRPC(t, f, "workspace.resolve", map[string]any{"path": "~/project/dest" + suffix}, &workspace); err != nil {
				t.Fatal(err)
			}
			realDestination, err := filepath.EvalSymlinks(filepath.Join(dir, "dest"+suffix))
			if err != nil || workspace.Cwd != realDestination {
				t.Fatalf("resolved workspace: %+v, %v", workspace, err)
			}
			if got := f.n.sessionWorkspaceCwd(filepath.Join(dir, "dest"+suffix)); got != realDestination {
				t.Fatalf("session workspace %q, want %q", got, realDestination)
			}
			var read struct {
				Text string `json:"text"`
			}
			if err := whitespaceRPC(t, f, "file.read", map[string]any{"path": "~/project/read" + suffix}, &read); err != nil || read.Text != "selected" {
				t.Fatalf("read selected: %+v, %v", read, err)
			}
			var chunk struct {
				Data []byte `json:"data"`
			}
			if err := whitespaceRPC(t, f, "file.chunk", map[string]any{"path": "~/project/read" + suffix, "offset": 0, "length": 8}, &chunk); err != nil || string(chunk.Data) != "selected" {
				t.Fatalf("chunk selected: %q, %v", chunk.Data, err)
			}
			var stats struct {
				Stats []struct {
					Path, Kind string
					Size       int64
				} `json:"stats"`
			}
			if err := whitespaceRPC(t, f, "fs.stat", map[string]any{"paths": []string{"~/project/read" + suffix}}, &stats); err != nil || len(stats.Stats) != 1 || stats.Stats[0].Size != 8 {
				t.Fatalf("stat selected: %+v, %v", stats, err)
			}
			var marker struct {
				Text string `json:"text"`
			}
			if err := whitespaceRPC(t, f, "file.read", map[string]any{"path": "~/project/dest" + suffix + "/marker"}, &marker); err != nil || marker.Text != "selected directory" {
				t.Fatalf("directory content: %+v, %v", marker, err)
			}

			makeFile(t, filepath.Join(dir, "rename"), "plain rename")
			makeFile(t, filepath.Join(dir, "rename"+suffix), "selected rename")
			var renamed struct {
				Path string `json:"path"`
			}
			if err := whitespaceRPC(t, f, "file.rename", map[string]any{"path": "~/project/rename" + suffix, "name": "renamed" + suffix}, &renamed); err != nil || renamed.Path != "~/project/renamed"+suffix {
				t.Fatalf("rename: %+v, %v", renamed, err)
			}
			assertWhitespaceText(t, filepath.Join(dir, "rename"), "plain rename")
			assertWhitespaceText(t, filepath.Join(dir, "renamed"+suffix), "selected rename")

			var moved struct {
				Moved []struct{ From, To string } `json:"moved"`
			}
			if err := whitespaceRPC(t, f, "file.move", map[string]any{"paths": []string{"~/project/read" + suffix}, "dir": "~/project/dest" + suffix}, &moved); err != nil {
				t.Fatal(err)
			}
			if len(moved.Moved) != 1 || moved.Moved[0].To != "~/project/dest"+suffix+"/read"+suffix {
				t.Fatalf("move response: %+v", moved)
			}
			assertWhitespaceText(t, plain, "plain")
			assertWhitespaceText(t, filepath.Join(dir, "dest"+suffix, "read"+suffix), "selected")
			if _, err := os.Lstat(filepath.Join(dir, "dest", "read"+suffix)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("unselected destination changed: %v", err)
			}

			makeFile(t, filepath.Join(dir, "trash"), "plain trash")
			makeFile(t, filepath.Join(dir, "trash"+suffix), "selected trash")
			var trashed struct {
				Trashed []struct {
					Path string `json:"path"`
					ID   string `json:"trash_id"`
				} `json:"trashed"`
			}
			if err := whitespaceRPC(t, f, "file.trash", map[string]any{"paths": []string{"~/project/trash" + suffix}}, &trashed); err != nil || len(trashed.Trashed) != 1 {
				t.Fatalf("trash: %+v, %v", trashed, err)
			}
			assertWhitespaceText(t, filepath.Join(dir, "trash"), "plain trash")
			if _, err := os.Lstat(filepath.Join(dir, "trash"+suffix)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("selected entry remains: %v", err)
			}
			if err := whitespaceRPC(t, f, "file.restore", map[string]any{"trash_ids": []string{trashed.Trashed[0].ID}}, nil); err != nil {
				t.Fatal(err)
			}
			assertWhitespaceText(t, filepath.Join(dir, "trash"+suffix), "selected trash")

			id := f.begin(t, map[string]any{"name": "upload" + suffix, "size": 3, "dest": "dir", "dir": "~/project/dest" + suffix, "conflict": "replace"})
			if err := f.call("chunk", map[string]any{"upload_id": id, "offset": 0, "data": []byte("new")}, nil); err != nil {
				t.Fatal(err)
			}
			var uploaded struct{ Path, Name string }
			if err := f.call("end", map[string]any{"upload_id": id}, &uploaded); err != nil {
				t.Fatal(err)
			}
			if uploaded.Name != "upload"+suffix {
				t.Fatalf("uploaded name: %+v", uploaded)
			}
			assertWhitespaceText(t, filepath.Join(dir, "dest", "upload"), "untouched")
			assertWhitespaceText(t, filepath.Join(dir, "dest"+suffix, "upload"), "untouched in selected directory")
			assertWhitespaceText(t, filepath.Join(dir, "dest"+suffix, "upload"+suffix), "new")
		})
	}
}

func TestWhitespaceOnlyPathsAndMissingNeighborsRPC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this control includes an ASCII-space-only filename")
	}
	f, dir := whitespaceHome(t)
	makeFile(t, filepath.Join(dir, " "), "spaces")
	if err := os.Mkdir(filepath.Join(dir, "\u00a0"), 0o700); err != nil {
		t.Fatal(err)
	}
	var listing struct {
		Path string `json:"path"`
	}
	if err := whitespaceRPC(t, f, "fs.list", map[string]any{"path": "", "cwd": dir}, &listing); err != nil || listing.Path != dir {
		t.Fatalf("empty list path: %+v, %v", listing, err)
	}
	if err := whitespaceRPC(t, f, "fs.list", map[string]any{"path": "\u00a0", "cwd": dir}, &listing); err != nil || listing.Path != filepath.Join(dir, "\u00a0") {
		t.Fatalf("whitespace directory: %+v, %v", listing, err)
	}
	var read struct {
		Text string `json:"text"`
	}
	if err := whitespaceRPC(t, f, "file.read", map[string]any{"path": " ", "cwd": dir}, &read); err != nil || read.Text != "spaces" {
		t.Fatalf("whitespace file: %+v, %v", read, err)
	}
	if err := whitespaceRPC(t, f, "file.read", map[string]any{"path": "", "cwd": dir}, &read); err == nil {
		t.Fatal("empty read was accepted")
	}
	if err := whitespaceRPC(t, f, "file.rename", map[string]any{"path": "", "cwd": dir, "name": "other"}, nil); err == nil {
		t.Fatal("empty rename targeted the current directory")
	}
	if err := whitespaceRPC(t, f, "file.move", map[string]any{"paths": []string{" "}, "cwd": dir, "dir": "\u00a0"}, nil); err != nil {
		t.Fatal(err)
	}
	var trashed struct {
		Trashed []struct {
			ID string `json:"trash_id"`
		} `json:"trashed"`
	}
	if err := whitespaceRPC(t, f, "file.trash", map[string]any{"paths": []string{"~/project/\u00a0/ "}}, &trashed); err != nil || len(trashed.Trashed) != 1 {
		t.Fatalf("trash whitespace-only name: %+v, %v", trashed, err)
	}
	if err := whitespaceRPC(t, f, "file.restore", map[string]any{"trash_ids": []string{trashed.Trashed[0].ID}}, nil); err != nil {
		t.Fatal(err)
	}
	assertWhitespaceText(t, filepath.Join(dir, "\u00a0", " "), "spaces")

	makeFile(t, filepath.Join(dir, "missing"), "neighbor")
	if err := os.Mkdir(filepath.Join(dir, "missing-dir"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		method string
		params map[string]any
	}{
		{"file.read", map[string]any{"path": "~/project/missing "}},
		{"file.rename", map[string]any{"path": "~/project/missing ", "name": "other"}},
		{"file.move", map[string]any{"paths": []string{"~/project/missing "}, "dir": "~/project/\u00a0"}},
		{"file.trash", map[string]any{"paths": []string{"~/project/missing "}}},
		{"fs.list", map[string]any{"path": "~/project/missing-dir "}},
		{"file.move", map[string]any{"paths": []string{"~/project/missing"}, "dir": "~/project/missing-dir "}},
	} {
		if err := whitespaceRPC(t, f, c.method, c.params, nil); err == nil {
			t.Errorf("%s accepted a missing path", c.method)
		}
	}
	assertWhitespaceText(t, filepath.Join(dir, "missing"), "neighbor")
}

func TestWhitespacePathGuardsRPC(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink permissions and trailing spaces differ on Windows")
	}
	f, dir := whitespaceHome(t)
	outside := t.TempDir()
	makeFile(t, filepath.Join(outside, "item"), "outside")
	if err := os.Symlink(outside, filepath.Join(dir, "link ")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	makeFile(t, filepath.Join(f.n.home, ".ssh", "key "), "secret")
	for _, c := range []struct {
		method string
		params map[string]any
	}{
		{"file.read", map[string]any{"path": "~/.ssh/key "}},
		{"file.rename", map[string]any{"path": "~/project/link /item", "name": "other"}},
		{"file.move", map[string]any{"paths": []string{"~/project/link /item"}, "dir": "~/project"}},
		{"file.trash", map[string]any{"paths": []string{"~/project/link /item"}}},
	} {
		if err := whitespaceRPC(t, f, c.method, c.params, nil); err == nil {
			t.Errorf("%s bypassed a path guard", c.method)
		}
	}
	assertWhitespaceText(t, filepath.Join(outside, "item"), "outside")
	if err := whitespaceRPC(t, f, "file.rename", map[string]any{"path": "~/project/link ", "name": "renamed link"}, nil); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(dir, "renamed link")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("link was not renamed as a link: %v", err)
	}
	assertWhitespaceText(t, filepath.Join(outside, "item"), "outside")
}

func TestFileEditRootAndHomeRefusedRPC(t *testing.T) {
	f, dir := whitespaceHome(t)
	root := filepath.VolumeName(dir) + string(os.PathSeparator)
	makeFile(t, filepath.Join(dir, "marker"), "untouched")
	for _, path := range []string{root, f.n.home} {
		for _, c := range []struct {
			method string
			params map[string]any
		}{
			{"file.rename", map[string]any{"path": path, "name": "other"}},
			{"file.move", map[string]any{"paths": []string{path}, "dir": "~/project"}},
			{"file.trash", map[string]any{"paths": []string{path}}},
		} {
			if err := whitespaceRPC(t, f, c.method, c.params, nil); err == nil {
				t.Errorf("%s accepted root or home %q", c.method, path)
			}
		}
	}
	assertWhitespaceText(t, filepath.Join(dir, "marker"), "untouched")
}

func TestWindowsWhitespaceComponentDetection(t *testing.T) {
	for _, tc := range []struct {
		path string
		want bool
	}{
		{`C:\work\note `, true}, {`C:\work \note`, true}, {"C:\\work\\note\u00a0", false},
		{`C:\work\note`, false}, {`C:\work\ `, true},
	} {
		if got := windowsPathHasTrailingSpace(tc.path); got != tc.want {
			t.Errorf("%q: got %v, want %v", tc.path, got, tc.want)
		}
	}
	if err := checkPathForOS(`C:\work\item `, "windows"); !errors.Is(err, errOffLimits) {
		t.Fatalf("Windows refusal must remain a guard error: %v", err)
	}
}
