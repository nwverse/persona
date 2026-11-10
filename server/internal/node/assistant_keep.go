package node

import "sync"

// A resident node keeps its headless conversations when the hub's connection ends (docs/operations/hub-restart.md "Chats not interrupted: hosting by the resident node"):
// a hub restart or a reconnect replaces the connection, and with it the Node serving it (attach.go), but the assistant
// processes go on — their turn, their background tasks and monitors — and the Node serving the next connection takes
// them over: what they say from then on reaches the new hub, the answer being generated included. Without a resident
// node (`node --stdio` serving the hub itself) the process ends with the connection, and so do they, as before.

// liveAssistants is every headless assistant this process runs, whichever Node it reports to (AssistantsBusy).
var liveAssistants sync.Map // *assistantProc -> struct{}

// AssistantsBusy: a conversation is answering or waiting on its background tasks or monitors; a resident node asked
// to restart (SIGHUP, a deploy) waits until none is (cmd/personad runResident). Idle ones resume on the next message.
func AssistantsBusy() bool {
	busy := false
	liveAssistants.Range(func(k, _ any) bool {
		p := k.(*assistantProc)
		p.mu.Lock()
		busy = p.alive() && (p.busy || p.compacting || len(p.tasks) > 0)
		p.mu.Unlock()
		return !busy
	})
	return busy
}

// stopAll ends what is kept: the resident node itself is ending.
func (k *assistantKeeper) stopAll() {
	k.mu.Lock()
	kept := k.procs
	k.procs = map[string]*assistantProc{}
	k.mu.Unlock()
	for _, p := range kept {
		p.stop()
	}
}

// assistantKeeper holds the conversations between two connections of a resident node.
type assistantKeeper struct {
	mu    sync.Mutex
	procs map[string]*assistantProc
	// gp is the process's gate proxy: the conversations kept hold their addresses at it (Node.gates, gateproxy.go).
	gp *gateProxy
}

func newAssistantKeeper() *assistantKeeper {
	return &assistantKeeper{procs: map[string]*assistantProc{}}
}

// keep takes the conversations of a Node whose connection ended; the ones it cannot keep are returned, to be stopped.
func (k *assistantKeeper) keep(procs map[string]*assistantProc) (stop []*assistantProc) {
	k.mu.Lock()
	defer k.mu.Unlock()
	for id, p := range procs {
		if (p.agent == "claude" || p.agent == "codex") && p.alive() {
			k.procs[id] = p
		} else {
			stop = append(stop, p)
		}
	}
	return stop
}

// adopt hands what is kept to the Node serving the new connection.
func (k *assistantKeeper) adopt(n *Node) []*assistantProc {
	k.mu.Lock()
	kept := k.procs
	k.procs = map[string]*assistantProc{}
	k.mu.Unlock()
	var out []*assistantProc
	n.mu.Lock()
	for id, p := range kept {
		if !p.alive() {
			continue
		}
		p.owner.Store(n)
		n.procs[id] = p
		if p.cx != nil {
			n.akeys[p.cx.key] = p
		}
		out = append(out, p)
	}
	n.mu.Unlock()
	return out
}

// forget drops a kept process that exited between two connections.
func (k *assistantKeeper) forget(p *assistantProc) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if k.procs[p.id] == p {
		delete(k.procs, p.id)
	}
}

// ownerOr is the Node a process reports to: the one serving the hub now if it was taken over, else n (which started
// it).
func (p *assistantProc) ownerOr(n *Node) *Node {
	if o := p.owner.Load(); o != nil {
		return o
	}
	return n
}

// resumeAssistants: the Node serving a new connection took kept conversations over. Their state goes to the hub
// (which lost nothing it had been told, but this Node's state starts from what the hub has), and the text being
// generated is sent again so an app that reconnects sees the answer go on where it was.
func (n *Node) resumeAssistants(adopted []*assistantProc) {
	for _, p := range adopted {
		p.mu.Lock()
		tasks := append([]string(nil), p.tasks...)
		text := p.partial.String()
		busy := p.busy
		p.mu.Unlock()
		n.rememberTasks(p.id, tasks)
		if busy && text != "" {
			n.emitDelta(p.id, text)
		}
	}
	if len(adopted) > 0 {
		n.logger.Printf("took over %d running conversations from the last hub connection", len(adopted))
		n.notifySessions()
	}
}

// handOverAssistants leaves this Node's conversations to the next connection (Serve's end, before the next one
// starts: attach.go waits for it) and stops what cannot be kept.
func (n *Node) handOverAssistants() {
	n.mu.Lock()
	stop := n.keeper.keep(n.procs)
	n.procs = map[string]*assistantProc{}
	n.akeys = map[string]*assistantProc{}
	n.mu.Unlock()
	for _, p := range stop {
		p.stop()
	}
}
