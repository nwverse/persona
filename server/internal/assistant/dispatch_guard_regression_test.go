package assistant

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/i18n"
	"github.com/nwverse/persona/server/internal/proto"
)

func TestDispatchGuardMalformedWritesKeepExistingPolicy(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	putGuard(t, s, guardPolicy(t, "task_plan"))
	st := s.store().(*memStore)
	beforePuts := st.puts
	_, changed := s.mcpChanges.snapshot()
	bodies := []string{
		`{}`,
		`null`,
		`{"dispatch_guard":null}`,
		`{"dispatch_gurad":{"enabled":true}}`,
		`{"dispatch_guard":{"enabeld":true}}`,
		`{"dispatch_guard":{"enabled":true}}`,
		`{"dispatch_guard":{"enabled":true,"targets":null}}`,
		`{"dispatch_guard":{"enabled":true,"targets":[{"machine_id":"server","session_id":"session-guarded","deny":["task_plan"],"unknown":true}]}}`,
		`{"dispatch_guard":{"enabled":true,"targets":[]},"extra":true}`,
	}
	for _, body := range bodies {
		t.Run(body, func(t *testing.T) {
			w := serveAs(s, roleFull, http.MethodPut, "/api/v1/assistant/dispatch-guard", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("malformed write returned %d %s", w.Code, w.Body.String())
			}
		})
	}
	if st.puts != beforePuts {
		t.Fatalf("malformed writes changed the store: puts %d -> %d", beforePuts, st.puts)
	}
	select {
	case <-changed:
		t.Fatal("a malformed write published a tool-list change")
	default:
	}
	if err := s.guardRefusal(context.Background(), guardMachine, guardSession, "task_plan"); err == nil {
		t.Fatal("a malformed write disabled the existing guard")
	}
}

func TestDispatchGuardDuplicateRawValuesKeepExistingPolicy(t *testing.T) {
	bodies := []string{
		`{"dispatch_guard":{"enabled":true,"unknown":1},"dispatch_guard":{"targets":[]}}`,
		`{"dispatch_guard":{"enabled":true,"targets":[{"unknown":1}],"targets":[]}}`,
		`{"dispatch_guard":{"enabled":true,"targets":null,"targets":[]}}`,
	}
	for _, role := range []string{roleFull, roleExtension} {
		for _, body := range bodies {
			t.Run(role+":"+body, func(t *testing.T) {
				f := newGuardFakeHub()
				s := newTestService(t, f)
				initial, _ := json.Marshal(guardPolicy(t, "task_plan"))
				w := serveAs(s, roleFull, http.MethodPut, dispatchGuardRoute, `{"dispatch_guard":`+string(initial)+`}`)
				if w.Code != http.StatusOK {
					t.Fatalf("activate: %d %s", w.Code, w.Body.String())
				}
				st := s.store().(*memStore)
				st.mu.Lock()
				beforePuts, beforeRaw := st.puts, string(st.vals[dispatchGuardSetting])
				st.mu.Unlock()
				_, changed := s.mcpChanges.snapshot()
				w = serveAs(s, role, http.MethodPut, dispatchGuardRoute, body)
				if w.Code != http.StatusBadRequest {
					t.Fatalf("duplicate raw value returned %d %s", w.Code, w.Body.String())
				}
				st.mu.Lock()
				same := st.puts == beforePuts && string(st.vals[dispatchGuardSetting]) == beforeRaw
				st.mu.Unlock()
				if !same {
					t.Fatal("duplicate raw value mutated storage")
				}
				select {
				case <-changed:
					t.Fatal("duplicate raw value notified discovery")
				default:
				}
				if err := s.guardRefusal(context.Background(), guardMachine, guardSession, "task_plan"); err == nil {
					t.Fatal("duplicate raw value disabled the existing guard")
				}
			})
		}
	}
}

func TestDispatchGuardEnabledEmptyTargetsClears(t *testing.T) {
	for _, role := range []string{roleFull, roleExtension} {
		t.Run(role, func(t *testing.T) {
			f := newGuardFakeHub()
			s := newTestService(t, f)
			putGuard(t, s, guardPolicy(t, "task_plan"))
			_, changed := s.mcpChanges.snapshot()
			body := `{"dispatch_guard":{"enabled":true,"targets":[]}}`
			w := serveAs(s, role, http.MethodPut, "/api/v1/assistant/dispatch-guard", body)
			if w.Code != http.StatusOK {
				t.Fatalf("enabled empty targets: %d %s", w.Code, w.Body.String())
			}
			var out struct {
				Guard DispatchGuard `json:"dispatch_guard"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
				t.Fatal(err)
			}
			if out.Guard.Enabled || out.Guard.ExpiresAt != nil || len(out.Guard.Targets) != 0 {
				t.Fatalf("clear was not normalized: %+v", out.Guard)
			}
			select {
			case <-changed:
			default:
				t.Fatal("clear did not publish a tool-list change")
			}
			if err := s.guardRefusal(context.Background(), guardMachine, guardSession, "task_plan"); err != nil {
				t.Fatalf("the cleared guard still refuses: %v", err)
			}
		})
	}
}

type guardLangWriter struct {
	*httptest.ResponseRecorder
	lang i18n.Lang
}

func (w guardLangWriter) Lang() i18n.Lang { return w.lang }

func TestDispatchGuardValidationResponseLanguage(t *testing.T) {
	for _, lang := range []i18n.Lang{i18n.En, i18n.ZhHans, i18n.Ja} {
		t.Run(string(lang), func(t *testing.T) {
			s := newTestService(t, newGuardFakeHub())
			r := httptest.NewRequest(http.MethodPut, dispatchGuardRoute, strings.NewReader(`{"dispatch_guard":null}`))
			r.Header.Set(RoleHeader, roleFull)
			r.Header.Set("Accept-Language", string(lang))
			w := guardLangWriter{ResponseRecorder: httptest.NewRecorder(), lang: lang}
			s.ServeHTTP(w, r)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status %d", w.Code)
			}
			want := i18n.T(lang, "assistant-dispatch_guard.required")
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("localized response %q does not contain %q", w.Body.String(), want)
			}
		})
	}
}

func discoveryTools(t *testing.T, out map[string]any) ([]proto.Tool, string) {
	t.Helper()
	if out["unchanged"] == true {
		t.Fatalf("watch stayed unchanged: %+v", out)
	}
	tools, ok := out["tools"].([]proto.Tool)
	if !ok {
		t.Fatalf("watch returned no full tool list: %+v", out)
	}
	revision, _ := out["revision"].(string)
	if revision == "" {
		t.Fatalf("watch returned no revision: %+v", out)
	}
	return tools, revision
}

func guardHeartbeatEvents(t *testing.T) chan string {
	t.Helper()
	events := make(chan string, 4)
	previousEvent := mcpToolsWatchEvent
	mcpToolsWatchEvent = func(event string) { events <- event }
	heartbeat := mcpToolsHeartbeat
	mcpToolsHeartbeat = 20 * time.Millisecond
	t.Cleanup(func() {
		mcpToolsWatchEvent = previousEvent
		mcpToolsHeartbeat = heartbeat
	})
	return events
}

func requireWatchEvent(t *testing.T, events chan string, want string) {
	t.Helper()
	select {
	case got := <-events:
		if got != want {
			t.Fatalf("watch event %q, want %q", got, want)
		}
	case <-time.After(time.Second):
		t.Fatalf("watch did not emit %q", want)
	}
}

func TestDispatchGuardNaturalExpiryUpdatesRevisionAwareList(t *testing.T) {
	f := newGuardFakeHub()
	s := newTestService(t, f)
	full := s.sessionMCPTools(context.Background(), guardMachine, guardSession, true, false)
	now := time.Now().UTC()
	advance := make(chan time.Time, 1)
	var current atomic.Value
	current.Store(now)
	originalNow := dispatchGuardNow
	dispatchGuardNow = func() time.Time {
		select {
		case next := <-advance:
			current.Store(next)
			return next
		default:
			return current.Load().(time.Time)
		}
	}
	t.Cleanup(func() { dispatchGuardNow = originalNow })
	expires := now.Add(time.Minute)
	g := guardPolicy(t, "task_plan")
	g.ExpiresAt = &expires
	putGuard(t, s, g)

	before, revision := discoveryTools(t, s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, ""))
	if slices.Contains(guardNamesOf(before), "task_plan") {
		t.Fatal("the enabled guard did not narrow the initial list")
	}
	events := guardHeartbeatEvents(t)
	watch := make(chan map[string]any, 1)
	go func() {
		watch <- s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, revision)
	}()
	requireWatchEvent(t, events, "wait")
	advance <- now.Add(2 * time.Minute)
	requireWatchEvent(t, events, "heartbeat")
	after, nextRevision := discoveryTools(t, <-watch)
	if nextRevision == revision {
		t.Fatal("natural expiry did not change the discovery revision")
	}
	if !reflect.DeepEqual(full, after) {
		t.Fatalf("expiry did not restore the complete original set: got %d tools, want %d", len(after), len(full))
	}
}

func TestDispatchGuardReadRecoveryUpdatesRevisionAwareList(t *testing.T) {
	f := newGuardFakeHub()
	st := &guardRecoveryStore{memStore: &memStore{vals: map[string][]byte{}}}
	s := newServiceOn(t, f, st)
	full := s.sessionMCPTools(context.Background(), guardMachine, guardSession, true, false)
	st.down.Store(true)
	_, revision := discoveryTools(t, s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, ""))
	events := guardHeartbeatEvents(t)
	watch := make(chan map[string]any, 1)
	go func() {
		watch <- s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, revision)
	}()
	requireWatchEvent(t, events, "wait")
	st.down.Store(false)
	requireWatchEvent(t, events, "heartbeat")
	after, nextRevision := discoveryTools(t, <-watch)
	if nextRevision == revision {
		t.Fatal("read recovery did not change the discovery revision")
	}
	if !reflect.DeepEqual(full, after) {
		t.Fatalf("read recovery did not restore the complete original set: got %d tools, want %d", len(after), len(full))
	}
}

type guardRecoveryStore struct {
	*memStore
	down atomic.Bool
}

func (s *guardRecoveryStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == dispatchGuardSetting && s.down.Load() {
		return false, errors.New("temporary guard read failure")
	}
	return s.memStore.Setting(ctx, key, out)
}

type guardSecondReadStore struct {
	*memStore
	reads atomic.Int32
}

func (s *guardSecondReadStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == dispatchGuardSetting && s.reads.Add(1) == 2 {
		return false, errors.New("one-read outage after a healthy admission read")
	}
	return s.memStore.Setting(ctx, key, out)
}

func TestDispatchGuardSecondReadFailureRecoversWholeSet(t *testing.T) {
	st := &guardSecondReadStore{memStore: &memStore{vals: map[string][]byte{}}}
	s := newServiceOn(t, newGuardFakeHub(), st)
	full := s.sessionMCPTools(context.Background(), guardMachine, guardSession, true, false)
	st.reads.Store(0)
	before, revision := discoveryTools(t, s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, ""))
	if slices.Contains(guardNamesOf(before), "task_plan") {
		t.Fatal("the one-read outage did not narrow the initial list")
	}
	if got := st.reads.Load(); got != 2 {
		t.Fatalf("initial response sampled guard %d times, want 2", got)
	}
	after, nextRevision := discoveryTools(t, s.sessionMCPToolsSince(context.Background(), guardMachine, guardSession, true, false, revision))
	if nextRevision == revision {
		t.Fatal("the failed decision remained bound to the recovered list")
	}
	if !reflect.DeepEqual(full, after) {
		t.Fatalf("recovery did not restore the complete original set: got %d tools, want %d", len(after), len(full))
	}
}
