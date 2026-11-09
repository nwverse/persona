package terminalmaintenance

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"time"

	"github.com/nwverse/persona/server/internal/osproc"
)

const Usage = "usage: personad manager maintenance prepare --record PATH | observe --prepared PATH [--wait DURATION] | recovery-recipe --prepared PATH\nOnly recovery material and process observations; no exit, resume or update execution.\n"

func Main(ctx context.Context, args []string, out io.Writer) error {
	if len(args) == 0 {
		return errors.New(Usage)
	}
	if args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		_, err := io.WriteString(out, Usage)
		return err
	}
	action := args[0]
	if action != "prepare" && action != "observe" && action != "recovery-recipe" {
		return errors.New("unsupported maintenance action; native exit and restoration execution are not available")
	}
	fs := flag.NewFlagSet("manager maintenance "+action, flag.ContinueOnError)
	fs.SetOutput(out)
	var path string
	var wait time.Duration
	if action == "prepare" {
		fs.StringVar(&path, "record", "", "private recovery record; explicit fields are required")
	} else {
		fs.StringVar(&path, "prepared", "", "private prepared maintenance record")
		if action == "observe" {
			fs.DurationVar(&wait, "wait", 0, "bounded observation, zero to 30s; zero reads once")
		}
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if path == "" || fs.NArg() != 0 {
		return errors.New("explicit input path is required; positional arguments are not accepted")
	}
	if action == "prepare" {
		var record Record
		if err := readPrivateJSON(path, &record); err != nil {
			return err
		}
		process, err := readProcess(ctx, record.PID)
		if err != nil {
			return err
		}
		prepared, err := Prepare(record, process, time.Now())
		if err != nil {
			return err
		}
		after, err := readProcess(ctx, record.PID)
		if err != nil {
			return err
		}
		if after != process {
			return errors.New("process observation changed during preparation")
		}
		return json.NewEncoder(out).Encode(prepared)
	}
	var prepared Prepared
	if err := readPrivateJSON(path, &prepared); err != nil {
		return err
	}
	if action == "observe" {
		observation, err := Observe(ctx, prepared, wait)
		return errors.Join(err, json.NewEncoder(out).Encode(observation))
	}
	recipe, err := prepared.Recipe()
	if err != nil {
		return err
	}
	return json.NewEncoder(out).Encode(recipe)
}

func readPrivateJSON(path string, target any) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	st, err := file.Stat()
	if err != nil {
		return err
	}
	const maxRecord = 1 << 20
	if !st.Mode().IsRegular() || !osproc.OwnedByMe(st) || st.Mode().Perm()&0o077 != 0 || st.Size() > maxRecord {
		return errors.New("maintenance input must be a private regular file owned by this user, at most 1 MiB")
	}
	decode := json.NewDecoder(io.LimitReader(file, maxRecord+1))
	decode.DisallowUnknownFields()
	if err := decode.Decode(target); err != nil {
		return err
	}
	var extra any
	if err := decode.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("maintenance input must contain exactly one JSON value")
	}
	return nil
}
