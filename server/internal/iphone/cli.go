package iphone

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/nwverse/persona/server/internal/clitext"
)

// Usage is `personad iphone`'s part of the usage text.
const Usage = `  personad iphone list|pair|forget|address|find|call …
                           this computer's paired iPhones for phone control: pair one over USB once, then the node
                           reaches it on the network (docs/extensions/iphone-pairing.md; personad iphone -h)`

const commandUsage = `usage:
  personad iphone list [--json]
                           the iPhones paired with this computer and the ones plugged in over USB
  personad iphone pair [<udid>]
                           pair the iPhone plugged in over USB (the only one, or <udid>): unlock it, tap Trust and enter
                           its passcode when it asks; the record stays on this computer only
  personad iphone forget <udid>
                           remove the pairing from this computer
  personad iphone address <udid> <host[:port]> | --clear
                           where to reach it when Bonjour can't (a VPN address or name); --clear goes back to Bonjour
  personad iphone find [--json]
                           the iPhones advertising RemotePairing on this network (Bonjour), and which are paired here
  personad iphone call <udid> <op> [<json args>]
                           send one bridge operation through a session of this command's own, for checks`

// Command runs `personad iphone args…` and returns its exit code: 0 done, 1 failed, 2 usage.
func Command(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	return command(ctx, New(Dir()), args, stdout, stderr)
}

func command(ctx context.Context, m *Manager, args []string, stdout, stderr io.Writer) int {
	defer m.Close()
	m.logf = func(string, ...any) {} // the command reports for itself
	usage := func(msg string) int {
		if msg != "" {
			fmt.Fprintln(stderr, msg)
		}
		fmt.Fprintln(stderr, clitext.Named(commandUsage))
		return 2
	}
	if len(args) == 0 {
		return usage("")
	}
	if clitext.IsHelpArg(args[0]) || args[0] == "help" {
		fmt.Fprintln(stdout, clitext.Named(commandUsage))
		return 0
	}
	fail := func(err error) int {
		fmt.Fprintln(stderr, explain(err))
		return 1
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		fs := flag.NewFlagSet("iphone list", flag.ContinueOnError)
		fs.SetOutput(stderr)
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(rest); err != nil || fs.NArg() > 0 {
			return usage("")
		}
		devices := m.Devices(ctx)
		if *asJSON {
			return printJSON(stdout, devices)
		}
		if len(devices) == 0 {
			fmt.Fprintln(stdout, "No paired iPhone, and none plugged in over USB.")
			if !m.Bridge(ctx).Installed {
				fmt.Fprintln(stdout, explain(&Error{Code: CodeBridgeMissing}))
			}
			return 0
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "UDID\tNAME\tiOS\tPAIRED\tUSB\tADDRESS")
		for _, d := range devices {
			addr := d.ManualAddress
			if addr == "" && d.Paired {
				addr = "(Bonjour)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", d.UDID, d.Name, d.OSVersion, yesNo(d.Paired), yesNo(d.USB), addr)
		}
		w.Flush()
		return 0
	case "pair":
		if len(rest) > 1 {
			return usage("")
		}
		udid := ""
		if len(rest) == 1 {
			udid = rest[0]
		}
		if udid == "" {
			b, err := m.bridge()
			if err != nil {
				return fail(err)
			}
			lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			list, err := b.USBList(lctx)
			cancel()
			if err != nil {
				return fail(err)
			}
			switch len(list) {
			case 0:
				return fail(errors.New("no iPhone is plugged in over USB: connect it with a cable, unlock it and try again"))
			case 1:
				udid = list[0].UDID
			default:
				var names []string
				for _, d := range list {
					names = append(names, "  "+d.UDID+"  "+d.Name)
				}
				return fail(fmt.Errorf("several iPhones are plugged in; say which:\n%s", strings.Join(names, "\n")))
			}
		}
		if !ValidUDID(udid) {
			return usage(fmt.Sprintf("invalid device id %q", udid))
		}
		pctx, cancel := context.WithTimeout(ctx, pairTimeout)
		defer cancel()
		rec, err := m.Pair(pctx, udid, func(step string) { fmt.Fprintln(stdout, step+"…") })
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return fail(errors.New("the phone didn't confirm in time: unlock it, tap Trust and enter its passcode, then try again"))
			}
			return fail(err)
		}
		name := rec.Name
		if name == "" {
			name = rec.UDID
		}
		fmt.Fprintf(stdout, "Paired %s. The node finds it on the same Wi-Fi by Bonjour; elsewhere set its address with: %s phone address %s <host>\n", name, clitext.CommandName(), rec.UDID)
		return 0
	case "forget":
		if len(rest) != 1 {
			return usage("")
		}
		if _, err := m.store.Load(rest[0]); errors.Is(err, ErrNotPaired) {
			return fail(fmt.Errorf("%s isn't paired with this computer", rest[0]))
		}
		if err := m.Forget(rest[0]); err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, "Forgotten. To remove this computer from the phone too: Settings › General › Transfer or Reset iPhone › Reset › Reset Location & Privacy.")
		return 0
	case "address":
		if len(rest) != 2 {
			return usage("")
		}
		addr := rest[1]
		if addr == "--clear" {
			addr = ""
		}
		if err := m.SetAddress(rest[0], addr); err != nil {
			if errors.Is(err, ErrNotPaired) {
				return fail(fmt.Errorf("%s isn't paired with this computer", rest[0]))
			}
			return fail(err)
		}
		if addr == "" {
			fmt.Fprintln(stdout, "Cleared: the node finds it by Bonjour again.")
		} else {
			fmt.Fprintln(stdout, "Saved: the node tries "+addr+" first.")
		}
		return 0
	case "find":
		fs := flag.NewFlagSet("iphone find", flag.ContinueOnError)
		fs.SetOutput(stderr)
		asJSON := fs.Bool("json", false, "print JSON")
		if err := fs.Parse(rest); err != nil || fs.NArg() > 0 {
			return usage("")
		}
		services, err := m.browse(ctx, browseWait, nil)
		if err != nil {
			return fail(err)
		}
		recs, _ := m.store.List()
		paired := map[string]string{} // instance → UDID
		if match, err := m.matcher(); err == nil {
			for _, svc := range services {
				if !svc.usable() {
					continue
				}
				for _, r := range recs {
					path, _ := m.store.RecordPath(r.UDID)
					if ok, _ := match(ctx, path, svc); ok {
						paired[svc.Instance] = r.UDID
						break
					}
				}
			}
		}
		if *asJSON {
			return printJSON(stdout, services)
		}
		if len(services) == 0 {
			fmt.Fprintln(stdout, "No iPhone advertises RemotePairing on this network.")
			return 0
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "INSTANCE\tADDRESS\tPAIRED")
		for _, s := range services {
			addr := "-"
			if len(s.Addrs) > 0 {
				addr = hostPort(s.Addrs[0].String(), s.Port)
			}
			p := paired[s.Instance]
			if p == "" {
				p = "no"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", s.Instance, addr, p)
		}
		w.Flush()
		return 0
	case "call":
		if len(rest) < 2 || len(rest) > 3 {
			return usage("")
		}
		var args json.RawMessage
		if len(rest) == 3 {
			if !json.Valid([]byte(rest[2])) {
				return usage("<json args> isn't valid JSON")
			}
			args = json.RawMessage(rest[2])
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		defer cancel()
		result, err := m.Call(cctx, rest[0], rest[1], args)
		if err != nil {
			return fail(err)
		}
		fmt.Fprintln(stdout, string(result))
		return 0
	}
	return usage(fmt.Sprintf("unknown phone command %q", sub))
}

// explain is err for people, with what to do about the session's own codes.
func explain(err error) string {
	switch Code(err) {
	case CodeBridgeMissing:
		return "The device bridge (" + BridgeName + ") isn't installed on this computer: put it next to " + clitext.CommandName() + " or in PATH, or set " + BridgeEnv + "."
	case "trust_pending":
		return "The phone hasn't trusted this computer yet: unlock it, tap Trust and enter its passcode, then run pair again."
	case "locked":
		return "The phone is locked: unlock it and try again."
	}
	return err.Error()
}

func printJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}
