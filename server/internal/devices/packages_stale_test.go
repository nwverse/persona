package devices

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/nwverse/persona/server/internal/nodeproto"
	"github.com/nwverse/persona/server/internal/update/updatetest"
)

// A build left in the node binary dir without a version file passes for the hub's own version. After a hub update
// that did not replace it (a Windows build next to a release published without one, a build a deploy had not replaced
// yet), Redeploy sent the old build as the hub's version, and every node refused it at its self-check (#12958). Such a
// build is taken only when it can be the hub's version; otherwise the release of the hub's version is fetched.
func TestStaleLocalNodeBuildIsNotTakenForTheHubs(t *testing.T) {
	const goos, arch = "windows", "amd64"
	const hub, old = "0.9.2-202611221500", "0.9.1-202611190505"
	plat := goos + "-" + arch
	stale := updatetest.VersionedProgram(t, nodeproto.ProgramPersonad, goos, arch, old)
	current := updatetest.VersionedProgram(t, nodeproto.ProgramPersonad, goos, arch, hub)
	ctx := context.Background()

	t.Run("an older build is passed over for the release", func(t *testing.T) {
		site := newFakeRelease(t)
		site.publish(t, hub, overlapping(hub, 202611221500), false, map[string]string{"personad-" + plat + ".exe": current})
		d, dir, _ := packageDevices(t, hub, site.srv.URL)
		copyFile(t, stale, filepath.Join(dir, "personad-"+plat+".exe"))
		pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad)
		if err != nil || pkg.path != "" || pkg.Version != hub || pkg.Fallback || pkg.file.Name != "personad-"+plat+".exe.gz" {
			t.Fatalf("the stale local build was taken for %s: %+v %v", hub, pkg, err)
		}
	})

	t.Run("the hub's own build is taken, and seen at once", func(t *testing.T) {
		site := newFakeRelease(t) // no release of the hub's version: only the local build can do
		d, dir, _ := packageDevices(t, hub, site.srv.URL)
		local := filepath.Join(dir, "personad-"+plat+".exe")
		copyFile(t, stale, local)
		if pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad); err == nil && pkg.path == local {
			t.Fatalf("the stale build was taken: %+v", pkg)
		}
		// The deploy puts the hub's build in place: the lookup remembered a moment ago does not stand for ten minutes.
		copyFile(t, current, local)
		pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad)
		if err != nil || pkg.path != local || pkg.Version != hub {
			t.Fatalf("the hub's build put in place: %+v %v", pkg, err)
		}
	})

	t.Run("a build named without .exe", func(t *testing.T) {
		d, dir, _ := packageDevices(t, hub, "")
		legacy := filepath.Join(dir, "personad-"+plat)
		copyFile(t, stale, legacy)
		if pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad); err == nil {
			t.Fatalf("a stale build without .exe was taken: %+v", pkg)
		}
		copyFile(t, current, legacy)
		pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad)
		if err != nil || pkg.path != legacy || pkg.Version != hub {
			t.Fatalf("the hub's build without .exe: %+v %v", pkg, err)
		}
		// The current name goes first.
		named := filepath.Join(dir, "personad-"+plat+".exe")
		copyFile(t, current, named)
		if pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad); err != nil || pkg.path != named {
			t.Fatalf("with both names: %+v %v", pkg, err)
		}
	})

	t.Run("a version file still says which build it is", func(t *testing.T) {
		d, dir, _ := packageDevices(t, hub, "")
		local := filepath.Join(dir, "personad-"+plat+".exe")
		copyFile(t, stale, local)
		if err := os.WriteFile(local+".version", []byte(old+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		pkg, err := d.nodePackageFor(ctx, goos, arch, nodeproto.ProgramPersonad)
		if err != nil || pkg.path != local || pkg.Version != old {
			t.Fatalf("a labelled build: %+v %v", pkg, err)
		}
	})
}

func TestFileContainsAcrossChunks(t *testing.T) {
	path := filepath.Join(t.TempDir(), "build")
	needle := "0.9.2-202611221500"
	body := make([]byte, 3<<20)
	at := 1<<20 - 5 // straddles the first chunk's end
	copy(body[at:], needle)
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	if ok, err := fileContains(path, []byte(needle)); err != nil || !ok {
		t.Fatalf("across a chunk boundary: %v %v", ok, err)
	}
	if ok, err := fileContains(path, []byte("0.9.1-202611190505")); err != nil || ok {
		t.Fatalf("absent: %v %v", ok, err)
	}
}
