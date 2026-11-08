package terminalmaintenance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCLIReadOnlyRoundTrip(t *testing.T) {
	r, _ := fixture(t, "codex")
	r.PID = os.Getpid()
	input := filepath.Join(r.Cwd, "record.json")
	data, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	write(t, input, string(data))
	var out bytes.Buffer
	if err := Main(context.Background(), []string{"prepare", "--record", input}, &out); err != nil {
		t.Fatal(err)
	}
	var p Prepared
	if err := json.Unmarshal(out.Bytes(), &p); err != nil {
		t.Fatal(err)
	}
	if p.Record.Session != testID || p.Capabilities != capabilities() {
		t.Fatalf("%+v", p)
	}
	path := filepath.Join(r.Cwd, "prepared.json")
	write(t, path, out.String())
	out.Reset()
	if err := Main(context.Background(), []string{"observe", "--prepared", path}, &out); err != nil {
		t.Fatal(err)
	}
	var observation Observation
	if err := json.Unmarshal(out.Bytes(), &observation); err != nil {
		t.Fatal(err)
	}
	if observation.State != "present" || observation.ProcessAbsent || observation.GracefulExitConfirmed || observation.MachineIdle {
		t.Fatalf("%+v", observation)
	}
	out.Reset()
	if err := Main(context.Background(), []string{"recovery-recipe", "--prepared", path}, &out); err != nil {
		t.Fatal(err)
	}
	var recipe RecoveryRecipe
	if err := json.Unmarshal(out.Bytes(), &recipe); err != nil {
		t.Fatal(err)
	}
	if recipe.ExecutionSupported || !reflect.DeepEqual(recipe.Arguments, []string{"resume", testID}) {
		t.Fatalf("%+v", recipe)
	}
	if err := p.Validate(); err != nil {
		t.Fatalf("CLI mutated original material: %v", err)
	}
	entries, err := os.ReadDir(r.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 4 {
		t.Fatalf("unexpected side effects: %v", entries)
	}
}

func TestAppendAfterDeparturePreservesPrefixAndChecksEveryID(t *testing.T) {
	for _, bad := range []bool{false, true} {
		t.Run(map[bool]string{false: "final-flush", true: "conflicting-final-flush"}[bad], func(t *testing.T) {
			r, proc := fixture(t, "claude")
			p := prepare(t, r, proc)
			out, err := observeWait(context.Background(), p, 0, func(context.Context, int) (Process, error) {
				id := testID
				if bad {
					id = "ffffffff-bbbb-cccc-dddd-eeeeeeeeeeee"
				}
				file, err := os.OpenFile(r.History, os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString(`{"sessionId":"` + id + `","type":"assistant"}` + "\n")
				closeErr := file.Close()
				if err != nil {
					t.Fatal(err)
				}
				if closeErr != nil {
					t.Fatal(closeErr)
				}
				return Process{PID: r.PID, UID: -1}, nil
			})
			if (err != nil) != bad || !out.ProcessAbsent || out.MachineIdle || out.GracefulExitConfirmed {
				t.Fatalf("%+v %v", out, err)
			}
			_, recipeErr := p.Recipe()
			if (recipeErr != nil) != bad {
				t.Fatalf("recipe err=%v", recipeErr)
			}
		})
	}
}

func TestSystemInstalledExecutableCanBePreserved(t *testing.T) {
	r, proc := fixture(t, "claude")
	path, err := exec.LookPath("sh")
	if err != nil {
		t.Fatal(err)
	}
	r.Executable, err = filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	p := prepare(t, r, proc)
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	if p.Capabilities.ResumeSupported {
		t.Fatal("file ownership became execution capability")
	}
}

func TestPrepareRejectsDuplicateAndExcessiveSettings(t *testing.T) {
	r, proc := fixture(t, "claude")
	for _, settings := range [][]string{{r.History}, {r.Executable}, {r.History, r.History}, make([]string, 17)} {
		r.Settings = settings
		if _, err := Prepare(r, proc, time.Now()); err == nil {
			t.Fatal("accepted repeated or excessive file set")
		}
	}
}

func TestProcessObservationIsIndependentOfTimezone(t *testing.T) {
	t.Setenv("TZ", "UTC")
	first, err := readProcess(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TZ", "Asia/Tokyo")
	second, err := readProcess(context.Background(), os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("process observation inherited local timezone")
	}
}

func TestChangedHistoryPrefixAndTruncationRefuseRecipe(t *testing.T) {
	for _, data := range []string{`{}`, strings.Repeat(" ", 20) + `{"sessionId":"` + testID + `","type":"user"}` + "\n"} {
		r, proc := fixture(t, "claude")
		p := prepare(t, r, proc)
		write(t, r.History, data)
		if _, err := p.Recipe(); err == nil {
			t.Fatal("lost original history prefix")
		}
	}
}
