package seam_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/assistant/kernel/seam"
)

func TestLoopNames(t *testing.T) {
	for _, name := range []string{"kb", "kb-history", "a2a"} {
		seam.NewLoopName(name)
	}
	raises(t, "seam.NewLoopName: a loop name is not empty", func() { seam.NewLoopName("") })
	for _, name := range []string{"KB", "kb_history", "kb history", "kb/x", "kbä"} {
		raises(t, "has a character outside [a-z0-9-]", func() { seam.NewLoopName(name) })
	}
}

// Each registered loop, whichever module registered it, starts on a goroutine of its own with the context StartLoops
// is handed, and runs until that context ends.
func TestRegisteredLoopsStart(t *testing.T) {
	type key struct{}
	started := make(chan string, 3)
	ended := make(chan string, 3)
	loop := func(name string) func(ctx context.Context) {
		return func(ctx context.Context) {
			started <- name + " " + ctx.Value(key{}).(string)
			<-ctx.Done()
			ended <- name
		}
	}
	reg := newPorts().registry()
	reg.Register(named("inbox", func(k seam.Kernel) {
		k.Loop(seam.NewLoopName("collect"), loop("collect"))
		k.Loop(seam.NewLoopName("report"), loop("report"))
	}))
	reg.Register(named("saved", func(seam.Kernel) {}))
	reg.Register(named("tasks", func(k seam.Kernel) { k.Loop(seam.NewLoopName("close"), loop("close")) }))
	reg.Seal()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), key{}, "run"))
	defer cancel()
	reg.StartLoops(ctx) // returns: each loop blocks on a goroutine of its own
	if got, want := names(t, started), []string{"close run", "collect run", "report run"}; !slices.Equal(got, want) {
		t.Errorf("started %v, want %v", got, want)
	}
	cancel()
	if got, want := names(t, ended), []string{"close", "collect", "report"}; !slices.Equal(got, want) {
		t.Errorf("ended %v, want %v", got, want)
	}
}

// names are the next three names on c, sorted, or the test fails after a few seconds.
func names(t *testing.T, c <-chan string) []string {
	t.Helper()
	var got []string
	for range 3 {
		select {
		case name := <-c:
			got = append(got, name)
		case <-time.After(5 * time.Second):
			t.Fatalf("after %v, no loop told within 5s", got)
		}
	}
	slices.Sort(got)
	return got
}

// A loop's name is taken once in a registry, whoever registers it, and a loop has a function and a made name.
func TestALoopIsRegisteredOnce(t *testing.T) {
	reg := newPorts().registry()
	run := func(context.Context) {}
	reg.Register(named("inbox", func(k seam.Kernel) {
		k.Loop(seam.NewLoopName("collect"), run)
		raises(t, `seam.Kernel.Loop: loop "collect" is registered twice, by "inbox" and "inbox"`, func() {
			k.Loop(seam.NewLoopName("collect"), run)
		})
		raises(t, `seam.Kernel.Loop: loop "report" has no function`, func() { k.Loop(seam.NewLoopName("report"), nil) })
		raises(t, "seam.Kernel.Loop: a LoopName comes from NewLoopName", func() { k.Loop(seam.LoopName{}, run) })
	}))
	reg.Register(named("tasks", func(k seam.Kernel) {
		raises(t, `seam.Kernel.Loop: loop "collect" is registered twice, by "inbox" and "tasks"`, func() {
			k.Loop(seam.NewLoopName("collect"), run)
		})
		k.Loop(seam.NewLoopName("close"), run)
	}))
}

// After Seal no loop registers, and a zero Kernel or Registry is a defect.
func TestNoLoopRegistersAfterSeal(t *testing.T) {
	var kept seam.Kernel
	reg := newPorts().registry()
	reg.Register(named("inbox", func(k seam.Kernel) { kept = k }))
	reg.Seal()
	run := func(context.Context) {}
	raises(t, "seam.Kernel.Loop: the registry is sealed", func() { kept.Loop(seam.NewLoopName("collect"), run) })
	var k seam.Kernel
	raises(t, "seam.Kernel.Loop: a Kernel comes from Registry.Register", func() { k.Loop(seam.NewLoopName("collect"), run) })
	var none *seam.Registry
	raises(t, "seam.Registry.StartLoops: a Registry comes from NewRegistry", func() { none.StartLoops(context.Background()) })
}
