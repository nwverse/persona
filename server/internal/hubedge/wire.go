// Package hubedge is the hub's edge (docs/operations/hub-restart.md "Blue-green switching: the hub's edge"): a thin process that holds the hub's
// listening sockets for as long as the service runs and relays every connection, byte for byte, to the hub process
// serving now (the active slot). A new hub binary is started next to the one serving, checks itself while the old one
// goes on, and only then do the two hand over: the edge holds new connections back for the second or two that takes
// (they wait, none is refused), the old one stops the way a planned stop does (hub_restarting, the machines and the
// event log handed over), the new one takes over, and the route moves to it. A new binary that cannot start never
// interrupts anything; one that fails while taking over is replaced by the binary that ran before.
//
// The edge itself is never updated by the hub: it changes only when the service restarts (a deploy whose edge
// protocol is newer, a changed unit). Everything else — the hub's state, its database, its TLS certificate and pin —
// stays the slot's: the edge relays TCP and says whom (a PROXY protocol v2 header on every relayed connection).
package hubedge

import (
	"bufio"
	"encoding/json"
	"io"
	"sync"
)

// Proto is the edge protocol this build speaks: what slots and `hub switch` say in hello / status. A slot speaks
// every protocol up to its own; an edge older than a binary's Proto is restarted by the deploy instead of switched.
const Proto = 1

// Message types between the edge and a slot (one JSON object per line, over two pipes the edge gives the slot), and
// between `personad hub switch` and the edge (over the control socket).
const (
	// slot -> edge
	MsgHello     = "hello"     // first thing: Proto, Version, Build, PID
	MsgStandby   = "standby"   // a standby slot checked itself (config, database) and waits for activate
	MsgReady     = "ready"     // listening on its sockets: the route may move to it
	MsgFailed    = "failed"    // could not start (Reason); it exits
	MsgSwitch    = "switch"    // the active slot asks to be replaced by Binary (Kind update or rollback)
	MsgConfirmed = "confirmed" // the update this slot is the result of is confirmed (selfupdate.go confirm)

	// edge -> slot
	MsgActivate     = "activate"      // a standby slot starts serving (its database migrations, the machines…)
	MsgRelease      = "release"       // stop now, the way a planned stop does; the process exits
	MsgSwitchFailed = "switch_failed" // the binary asked for did not come up (Reason): this slot stays active

	// hub switch <-> edge
	MsgStatus   = "status"   // request; the reply carries Proto, Version, Build, Binary, PID, Slot
	MsgProgress = "progress" // a switch's phase (Phase), while it runs
	MsgDone     = "done"     // a switch's end: OK, or Reason
)

// Kinds of switch.
const (
	KindUpdate   = "update"   // the hub's self-update: confirmed by the new slot, else rolled back (Expect is its build)
	KindRollback = "rollback" // back to the binary that ran before (the files are already restored)
	KindDeploy   = "deploy"   // a binary put in place by hand or by deploy.sh (hub switch)
)

// Msg is every message; T says which, the rest is what that one carries.
type Msg struct {
	T       string `json:"t"`
	Proto   int    `json:"proto,omitempty"`
	Version string `json:"version,omitempty"`
	Build   int64  `json:"build,omitempty"`
	PID     int    `json:"pid,omitempty"`
	Slot    string `json:"slot,omitempty"`
	Binary  string `json:"binary,omitempty"`
	Prev    string `json:"prev,omitempty"` // the binary to go back to when Binary fails while taking over
	Kind    string `json:"kind,omitempty"`
	Expect  int64  `json:"expect,omitempty"` // the build Binary must say it is (0: any)
	Phase   string `json:"phase,omitempty"`
	Edge    string `json:"edge,omitempty"` // status: the edge's own version (Version and Build are the serving slot's)
	OK      bool   `json:"ok,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// lineWriter writes messages whole, one per line, from any goroutine.
type lineWriter struct {
	mu sync.Mutex
	w  io.Writer
}

func (l *lineWriter) send(m Msg) error {
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, err = l.w.Write(append(b, '\n'))
	return err
}

// readMsgs sends each line r carries to out until r ends, then closes out. A line that is not a message is skipped.
func readMsgs(r io.Reader, out chan<- Msg) {
	defer close(out)
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 4096), 1<<20)
	for sc.Scan() {
		var m Msg
		if json.Unmarshal(sc.Bytes(), &m) == nil && m.T != "" {
			out <- m
		}
	}
}
