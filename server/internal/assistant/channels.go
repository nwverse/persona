package assistant

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nwverse/persona/server/internal/channel"
)

// The channels (docs/extensions/channels.md): the ways of talking to Persona from a chat app — the Feishu bot (feishu*.go) and
// the bots plugins contribute (the channel extension point, channels_plugin.go). A message from a channel goes into the
// conversation the channel talks to, the turn is followed and its answer sent back the same way (channels_runs.go);
// what is asked in a conversation a channel drives goes there too. All of that is the assistant's. The hub keeps the
// transport — the open platform's long connection, pairing, the plugins' programs — hands the assistant what comes in
// (channels.receive, feishu.message, feishu.card_action) and posts what it is given (app.call feishu.*, channels.*).
// While the assistant is not connected the hub answers the owner's messages "the assistant can't be reached right now" itself.

// The channels' state: each plugin channel's settings and the conversations its messages went to lately (channels),
// the runs, the asks waiting in text and the messages taken (channels.state). The same keys the hub kept before; only
// the assistant reads and writes them now.
const (
	channelsKey      = "channels"
	channelsStateKey = "channels.state"
)

type channelPrefs struct {
	Target          *channel.Target    `json:"target,omitempty"`
	ThreadMode      channel.ThreadMode `json:"thread_mode,omitempty"`
	ThreadIdleHours int                `json:"thread_idle_hours,omitempty"`
	Features        map[string]bool    `json:"features,omitempty"`
	// Recent are the conversations its messages went to lately (newest first, a few).
	Recent []channel.Conversation `json:"recent,omitempty"`
	// Threads are the conversations of its threads (capabilities.threads or integrations_v2 routing), by routing key.
	Threads map[string]*channelThread `json:"threads,omitempty"`
}

const channelRecentKeep = 5

// channelThread is the conversation one of a channel's threads talks to (the thread's first message started it), and
// when a message of the thread last went there.
type channelThread struct {
	MachineID string    `json:"machine_id"`
	SessionID string    `json:"session_id"`
	At        time.Time `json:"at"`
}

func (t *channelThread) isSameConversation(other *channelThread) bool {
	return t != nil && other != nil && t.SessionID != "" &&
		t.MachineID == other.MachineID && t.SessionID == other.SessionID
}

// A thread nobody wrote in for channelThreadsKeepFor starts a new conversation; past channelThreadsKeep the oldest go.
const (
	channelThreadsKeep    = 500
	channelThreadsKeepFor = 30 * 24 * time.Hour
)

type channelsState struct {
	Runs []*channel.Run `json:"runs,omitempty"`
	Asks []*channel.Ask `json:"asks,omitempty"`
	// Seen are, by channel, the ids of the latest messages taken (the chat apps deliver some again).
	Seen map[string][]string `json:"seen,omitempty"`
}

const channelSeenKeep = 300

// channelsBook is the channels' state in memory.
type channelsBook struct {
	mu sync.Mutex
	// receiving is the last accepted message for each channel/thread, until its route and binding are settled.
	receiving map[channelAdmissionKey]chan struct{}
	loaded    bool
	prefs     map[string]*channelPrefs
	// st is what the runs wait on (channels_runs.go), loaded apart from prefs.
	st       channelsState
	stLoaded bool
	live     map[string]*chanLive // run id -> its live progress (not persisted)
	// A topic tool can arrive before the node acknowledges the message that prompted it.
	topicStarting map[string]chan struct{}
	// answered: conversation -> when its last run was answered (no "finished" push, no mirror for it)
	answered map[[2]string]time.Time
	// notified: channel + conversation (machine, session) -> when its notify_user copy arrived
	notified map[[3]string]time.Time
	// answering: the runs whose answer is going out now (finishChannelRun): a later message is not folded into them
	answering map[string]bool
	// follows: how many runs follow each conversation (session.follow)
	follows map[[2]string]int
	// followRetry: the followed conversations whose session.follow failed -> when a run's watcher asks again
	followRetry map[[2]string]time.Time
	// announced: the cards this process heard of (interaction.announced, or pending when it first connected); one
	// that came while it was not connected is taken when it is back (channelsCatchUp). announcedOccurrence
	// distinguishes a same-ID replacement. A missing timestamp is legacy/held ID-only bookkeeping.
	announced           map[string]bool
	announcedOccurrence map[string]time.Time
	connected           bool
	// ended: the cards that ended lately (interaction.ended), by when: one still being sent to a channel then is closed
	// (or not asked) as soon as it is out. endedOccurrence distinguishes a same-ID replacement.
	ended           map[string]time.Time
	endedOccurrence map[string]time.Time
	// lastEnded: channel -> the latest text ask there that ended without being answered there (answered in the app,
	// in a terminal, withdrawn), by when: a late reply to it is told so instead of going into the conversation.
	lastEnded map[string]endedAsk
	// generation changes on unbind so an in-flight message or question cannot restore the old binding's state.
	generation map[string]uint64
	// list is the hub's list of the channels, kept a moment (the runs look at it every second).
	list         []channel.View
	listAt       time.Time
	overrides    map[string]*channel.TargetOverride
	hasOverrides bool
	now          func() time.Time // tests
}

func (c *channelsBook) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

func (s *Service) loadChannelsLocked(ctx context.Context) {
	c := &s.chans
	if c.loaded {
		return
	}
	c.prefs = map[string]*channelPrefs{}
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, channelsKey, &c.prefs); err != nil {
			s.logf("channels: load: %v", err)
			return // not marked loaded: the next look tries again
		}
	}
	if c.prefs == nil {
		c.prefs = map[string]*channelPrefs{}
	}
	c.loaded = true
}

var errChannelSettingsWrite = errors.New("channel settings could not be saved")

// saveChannelsLocked stages one channel's replacement while the book is locked and restores its previous entry
// on a failed write. Callers must copy any map or slice they edit before passing the replacement here.
func (s *Service) saveChannelsLocked(id string, next channelPrefs) error {
	c := &s.chans
	if !c.loaded {
		return errChannelsUnread
	}
	previous, existed := c.prefs[id]
	c.prefs[id] = &next
	st := s.store()
	if st == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, channelsKey, c.prefs); err != nil {
		if existed {
			c.prefs[id] = previous
		} else {
			delete(c.prefs, id)
		}
		s.logf("channels: save: %v", err)
		return fmt.Errorf("%w: %w", errChannelSettingsWrite, err)
	}
	return nil
}

func (c *channelsBook) prefsForUpdate(id string) channelPrefs {
	if p := c.prefs[id]; p != nil {
		return *p
	}
	return channelPrefs{}
}

// channelPrefLocked is channel id's settings (created on first use; the caller holds s.chans.mu).
func (s *Service) channelPrefLocked(id string) *channelPrefs {
	p := s.chans.prefs[id]
	if p == nil {
		p = &channelPrefs{}
		s.chans.prefs[id] = p
	}
	if p.Features == nil {
		p.Features = map[string]bool{}
		for k, v := range channel.DefaultFeatures {
			p.Features[k] = v
		}
	}
	return p
}

// loadChannelStateLocked reads the runs once (the caller holds s.chans.mu); the runs an older hub kept in the Feishu
// bot's state come over the first time.
//
// The state is decoded into a fresh value, never into c.st: runs and asks started while it could not be read are
// already in c.st, and their watchers read them without the lock. Decoding in place would reuse those pointers and
// write the stored runs' fields over them. They are kept next to what was stored instead.
func (s *Service) loadChannelStateLocked(ctx context.Context) {
	c := &s.chans
	if c.stLoaded {
		return
	}
	var stored channelsState
	if st := s.store(); st != nil {
		if _, err := st.Setting(ctx, channelsStateKey, &stored); err != nil {
			s.logf("channels: load state: %v", err)
			return
		}
	}
	if stored.Seen == nil {
		stored.Seen = map[string][]string{}
	}
	var kept int
	stored.Runs, kept = keepLive(stored.Runs, c.st.Runs, func(r *channel.Run) string { return r.ID })
	var asks int
	stored.Asks, asks = keepLive(stored.Asks, c.st.Asks, func(a *channel.Ask) string { return a.ID })
	c.st = stored
	c.stLoaded = true
	if kept+asks > 0 {
		s.saveChannelStateLocked()
		s.logf("channels: %d runs and %d asks started while the state was unreadable kept", kept, asks)
	}
	if old := s.larkModule().LegacyRuns(ctx); len(old) > 0 {
		c.st.Runs = append(c.st.Runs, old...)
		s.saveChannelStateLocked()
		s.logf("channels: %d waiting turns of the Feishu bot moved to the channels' runs", len(old))
	}
}

// keepLive is stored with the items held in memory added: one with the id of a stored item takes its place (it is
// the one being watched), the others are appended; n is how many came from memory.
func keepLive[T any](stored, live []*T, id func(*T) string) (out []*T, n int) {
	if len(live) == 0 {
		return stored, 0
	}
	at := make(map[string]int, len(stored))
	for i, x := range stored {
		at[id(x)] = i
	}
	for _, x := range live {
		if i, ok := at[id(x)]; ok {
			stored[i] = x
		} else {
			at[id(x)] = len(stored)
			stored = append(stored, x)
		}
		n++
	}
	return stored, n
}

// saveChannelStateLocked writes the runs, asks and seen messages, once they were read (the same as the settings).
func (s *Service) saveChannelStateLocked() error {
	st := s.store()
	if st == nil || !s.chans.stLoaded {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := st.PutSetting(ctx, channelsStateKey, s.chans.st); err != nil {
		s.logf("channels: save state: %v", err)
		return err
	}
	return nil
}

// bindChannelThread notes the conversation only while its accepted binding generation is current.
// Omitting generation is for seeding an existing binding; receive paths always supply their accepted generation.
func (s *Service) bindChannelThread(id string, keys channelThreadKeys, machine, session string, generation ...uint64) bool {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(generation) > 0 && c.generation[id] != generation[0] {
		return false
	}
	s.loadChannelsLocked(context.Background())
	p := c.prefsForUpdate(id)
	p.Threads = maps.Clone(p.Threads)
	if p.Threads == nil {
		p.Threads = map[string]*channelThread{}
	}
	now := c.clock().UTC()
	binding := &channelThread{MachineID: machine, SessionID: session, At: now}
	p.Threads[keys.primary] = binding
	// Once both keys have been used, keep them on the same conversation and idle clock.
	if keys.alternate != "" && p.Threads[keys.alternate] != nil {
		p.Threads[keys.alternate] = binding
	}
	keepFor := max(channelThreadsKeepFor, channelThreadIdleTTL(true, &p))
	pruneChannelThreadsWithTTL(p.Threads, now, keepFor)
	// The message was already delivered. Follow its turn even if the binding could not be saved.
	_ = s.saveChannelsLocked(id, p)
	return true
}

// unbindChannelThread forgets the topic's bindings so /new cannot fall back to the old conversation.
func (s *Service) unbindChannelThread(id string, keys channelThreadKeys, generation uint64) error {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation[id] != generation {
		return nil
	}
	s.loadChannelsLocked(context.Background())
	if !c.loaded {
		return errChannelsUnread
	}
	p := c.prefsForUpdate(id)
	primary, alternate := p.Threads[keys.primary], p.Threads[keys.alternate]
	if primary == nil && alternate == nil {
		return nil
	}
	p.Threads = maps.Clone(p.Threads)
	// A command may carry only one routing format. Clear the other keys for the same conversation too.
	maps.DeleteFunc(p.Threads, func(key string, binding *channelThread) bool {
		return key == keys.primary || (keys.alternate != "" && key == keys.alternate) ||
			binding.isSameConversation(primary) || binding.isSameConversation(alternate)
	})
	return s.saveChannelsLocked(id, p)
}

func (s *Service) setChannelTarget(ctx context.Context, id string, target channel.Target, generation uint64) error {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.generation[id] != generation {
		return nil
	}
	s.loadChannelsLocked(ctx)
	p := c.prefsForUpdate(id)
	p.Target = &target
	return s.saveChannelsLocked(id, p)
}

// channelThreadIdleTTL returns the inactivity timeout for a channel's routed thread/group/topic bindings.
// While integrations_v2 is off (or thread_idle_hours is 0), the legacy 30-day timeout applies.
func channelThreadIdleTTL(v2 bool, p *channelPrefs) time.Duration {
	if v2 && p != nil && p.ThreadIdleHours > 0 {
		return time.Duration(min(p.ThreadIdleHours, channel.MaxThreadIdleHours)) * time.Hour
	}
	return channelThreadsKeepFor
}

// channelTopicRouteKey returns a canonical, collision-free routing key for a (chat_id, topic_id) pair.
func channelTopicRouteKey(chatID, topicID string) string {
	return "v2:topic:" + strconv.Itoa(len(chatID)) + ":" + chatID + ":" + topicID
}

// channelGroupRouteKey returns a canonical routing key for a non-topic group chat.
func channelGroupRouteKey(chatID string) string {
	return "v2:group:" + chatID
}

func channelStructuredTopicKey(m channel.Message) string {
	chatID, topicID := strings.TrimSpace(m.ChatID), strings.TrimSpace(m.TopicID)
	if m.ChatKind.Known() == channel.ChatKindDirect || chatID == "" || topicID == "" {
		return ""
	}
	return channelTopicRouteKey(chatID, topicID)
}

// channelRouteKey computes the integrations_v2 conversation routing key for m under mode.
// Empty string means the message routes to the channel's configured default target.
func channelRouteKey(mode channel.ThreadMode, m channel.Message) string {
	if mode != channel.ThreadModePerGroupAndTopic && mode != channel.ThreadModePerTopic {
		return ""
	}
	kind := m.ChatKind.Known()
	if kind == channel.ChatKindDirect {
		return ""
	}
	if key := channelStructuredTopicKey(m); key != "" {
		return key
	}
	if strings.TrimSpace(m.TopicID) == "" && kind == channel.ChatKindGroup {
		if chatID := strings.TrimSpace(m.ChatID); mode == channel.ThreadModePerGroupAndTopic && chatID != "" {
			return channelGroupRouteKey(chatID)
		}
		return ""
	}
	// An unclassified message or an incomplete topic can still identify its legacy conversation.
	return strings.TrimSpace(m.Thread)
}

type channelThreadKeys struct {
	primary   string
	alternate string
}

// channelThreadAliases pairs the two keys only when the message identifies a topic in both forms
// and one of them is already the configured route.
func channelThreadAliases(primary string, m channel.Message) channelThreadKeys {
	keys := channelThreadKeys{primary: primary}
	legacy := strings.TrimSpace(m.Thread)
	if primary == "" || legacy == "" {
		return keys
	}
	topic := channelStructuredTopicKey(m)
	if topic == "" || topic == legacy {
		return keys
	}
	switch primary {
	case legacy:
		keys.alternate = topic
	case topic:
		keys.alternate = legacy
	}
	return keys
}

// channelThreadOf returns the live primary binding or, on a miss, the live alternate binding.
func (s *Service) channelThreadOf(ctx context.Context, id string, keys channelThreadKeys, v2 bool) *channelThread {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	s.loadChannelsLocked(ctx)
	p := s.channelPrefLocked(id)
	now, ttl := c.clock(), channelThreadIdleTTL(v2, p)
	for _, key := range [...]string{keys.primary, keys.alternate} {
		if t := p.Threads[key]; key != "" && t != nil && now.Sub(t.At) <= ttl {
			cp := *t
			return &cp
		}
	}
	return nil
}

// pruneChannelThreads drops the threads quiet for longer than channelThreadsKeepFor, then the oldest beyond
// channelThreadsKeep.
func pruneChannelThreads(threads map[string]*channelThread, now time.Time) {
	pruneChannelThreadsWithTTL(threads, now, channelThreadsKeepFor)
}

func pruneChannelThreadsWithTTL(threads map[string]*channelThread, now time.Time, keepFor time.Duration) {
	for key, t := range threads {
		if t == nil || now.Sub(t.At) > keepFor {
			delete(threads, key)
		}
	}
	if len(threads) <= channelThreadsKeep {
		return
	}
	keys := slices.Collect(maps.Keys(threads))
	slices.SortFunc(keys, func(a, b string) int { return threads[a].At.Compare(threads[b].At) })
	for _, key := range keys[:len(keys)-channelThreadsKeep] {
		delete(threads, key)
	}
}

// noteChannelConversation remembers that channel id's message went to a conversation (the detail's recent list, the
// conversation's source marks).
func (s *Service) noteChannelConversation(id, machine, session string) {
	c := &s.chans
	c.mu.Lock()
	s.loadChannelsLocked(context.Background())
	p := c.prefsForUpdate(id)
	p.Recent = slices.DeleteFunc(slices.Clone(p.Recent), func(x channel.Conversation) bool { return x.MachineID == machine && x.SessionID == session })
	p.Recent = append([]channel.Conversation{{MachineID: machine, SessionID: session, At: time.Now().UTC()}}, p.Recent...)
	if len(p.Recent) > channelRecentKeep {
		p.Recent = p.Recent[:channelRecentKeep]
	}
	_ = s.saveChannelsLocked(id, p) // Recent is bookkeeping for a message that already went through.
	c.mu.Unlock()
}

// channelsReport is what the hub shows of the channels (Snapshot.Channels): the channels each conversation's messages
// came from, the conversations a channel's turn is followed in or was answered in within the hour; nil while the
// state can't be read.
func (s *Service) channelsReport() *ChannelsReport {
	c := &s.chans
	c.mu.Lock()
	defer c.mu.Unlock()
	ctx := context.Background()
	s.loadChannelsLocked(ctx)
	s.loadChannelStateLocked(ctx)
	if !c.loaded || !c.stLoaded {
		return nil
	}
	out := &ChannelsReport{Marks: []ChannelMark{}, Turns: []ChannelTurn{}}
	type mark struct {
		id string
		at time.Time
	}
	marks := map[[2]string][]mark{}
	for id, p := range c.prefs {
		for _, r := range p.Recent {
			k := [2]string{r.MachineID, r.SessionID}
			marks[k] = append(marks[k], mark{id, r.At})
		}
	}
	for k, ms := range marks {
		slices.SortFunc(ms, func(a, b mark) int {
			if n := b.at.Compare(a.at); n != 0 {
				return n
			}
			return compareStrings(a.id, b.id)
		})
		m := ChannelMark{MachineID: k[0], SessionID: k[1]}
		for _, x := range ms {
			m.Channels = append(m.Channels, x.id)
		}
		out.Marks = append(out.Marks, m)
	}
	busy := map[[2]string]bool{}
	for _, r := range c.st.Runs {
		k := [2]string{r.MachineID, r.SessionID}
		if !busy[k] {
			busy[k] = true
			out.Turns = append(out.Turns, ChannelTurn{MachineID: k[0], SessionID: k[1]})
		}
	}
	now := c.clock()
	for k, at := range c.answered {
		if now.Sub(at) > time.Hour {
			delete(c.answered, k)
			continue
		}
		if !busy[k] {
			out.Turns = append(out.Turns, ChannelTurn{MachineID: k[0], SessionID: k[1], Answered: at.UTC()})
		}
	}
	slices.SortFunc(out.Marks, func(a, b ChannelMark) int {
		return compareStrings(a.MachineID+"/"+a.SessionID, b.MachineID+"/"+b.SessionID)
	})
	slices.SortFunc(out.Turns, func(a, b ChannelTurn) int {
		return compareStrings(a.MachineID+"/"+a.SessionID, b.MachineID+"/"+b.SessionID)
	})
	return out
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// channelsChanged reports the snapshot again: the hub shows the channels' marks and turns from it.
func (s *Service) channelsChanged() { go s.publish(context.Background()) }

// channelLoop sweeps every few seconds: turns whose watcher was lost (a restart), every channel's.
func (s *Service) channelLoop(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		s.sweepChannelRuns()
	}
}
