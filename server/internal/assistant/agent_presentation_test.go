package assistant

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
)

func presentationFixture(t *testing.T) (*fleetTest, proto.Session) {
	t.Helper()
	ft := newFleetTest(t)
	raw := proto.Session{SessionID: "s1", Title: "Default title", TitleSource: proto.TitleSourcePrompt}
	ft.sessions("mac", raw)
	return ft, raw
}

func TestAgentPresentationPersistsAndClearsWithoutRuntimeRename(t *testing.T) {
	ft, raw := presentationFixture(t)
	key := agentNameKey("mac", "s1")
	ft.s.agentNames.mu.Lock()
	if !ft.s.agentNamesLocked(context.Background()) {
		t.Fatal("load naming ledger")
	}
	if err := ft.s.putAgentNameLocked(context.Background(), key, agentNameRecord{Machine: "mac", Session: "s1", Title: "Automatic title", RawTitle: raw.Title, Changes: 1}); err != nil {
		t.Fatal(err)
	}
	ft.s.agentNames.mu.Unlock()

	name, tags := "  Release review  ", []string{" release ", "Review", "release", "iOS"}
	update, err := normalizeAgentPresentation(&name, &tags)
	if err != nil {
		t.Fatal(err)
	}
	row, err := ft.s.updateAgentPresentation(context.Background(), "mac", "s1", update)
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Release review" || row.DisplayName() != "Release review" || !reflect.DeepEqual(row.Tags(), []string{"release", "Review", "iOS"}) {
		t.Fatalf("decorated presentation: %+v", row)
	}
	if calls := ft.d.callsTo("mac", DeviceSessionRename); len(calls) != 0 {
		t.Fatalf("presentation called the native rename: %v", calls)
	}

	restarted := New(Options{Hub: ft.f, Store: func() Store { return ft.st }, Logf: testLogf(t), Devices: ft.d})
	row, ok := restarted.decoratedSession("mac", "s1")
	if !ok || row.Title != "Release review" || row.DisplayName() != "Release review" || len(row.Tags()) != 3 {
		t.Fatalf("presentation did not survive restart: %+v", row)
	}
	clearName, clearTags := "", []string{}
	update, err = normalizeAgentPresentation(&clearName, &clearTags)
	if err != nil {
		t.Fatal(err)
	}
	row, err = restarted.updateAgentPresentation(context.Background(), "mac", "s1", update)
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Automatic title" || row.DisplayName() != "" || len(row.Tags()) != 0 {
		t.Fatalf("clear did not restore automatic presentation: %+v", row)
	}
}

func TestAgentPresentationArchivedRowRestoresHiddenTitle(t *testing.T) {
	ft, _ := presentationFixture(t)
	name := "Custom title"
	update, err := normalizeAgentPresentation(&name, nil)
	if err != nil {
		t.Fatal(err)
	}
	row, err := ft.s.updateAgentPresentation(context.Background(), "mac", "s1", update)
	if err != nil {
		t.Fatal(err)
	}
	if err := ft.s.noteAgentArchive(context.Background(), row, true); err != nil {
		t.Fatal(err)
	}
	ft.d.setSessions("mac", nil)
	clear := ""
	update, err = normalizeAgentPresentation(&clear, nil)
	if err != nil {
		t.Fatal(err)
	}
	row, err = ft.s.updateAgentPresentation(context.Background(), "mac", "s1", update)
	if err != nil {
		t.Fatal(err)
	}
	if row.Title != "Default title" || row.DisplayName() != "" {
		t.Fatalf("archived clear kept the override: %+v", row)
	}
}

func TestNormalizeAgentPresentation(t *testing.T) {
	name := strings.Repeat("x", agentPresentationNameRunes+1)
	if _, err := normalizeAgentPresentation(&name, nil); err == nil {
		t.Fatal("long name accepted")
	}
	name = "two\nlines"
	if _, err := normalizeAgentPresentation(&name, nil); err == nil {
		t.Fatal("multiline name accepted")
	}
	tags := []string{"Release", "release", " review "}
	update, err := normalizeAgentPresentation(nil, &tags)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(*update.tags, []string{"Release", "review"}) {
		t.Fatalf("tag normalization: %v", *update.tags)
	}
	tags = make([]string, agentPresentationTagLimit+1)
	for i := range tags {
		tags[i] = strings.Repeat("x", i+1)
	}
	if _, err := normalizeAgentPresentation(nil, &tags); err == nil {
		t.Fatal("too many tags accepted")
	}
}

func TestAgentPresentationRoute(t *testing.T) {
	ft, _ := presentationFixture(t)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/sessions/mac/s1/presentation", strings.NewReader(`{"display_name":"Named","tags":["one","two"]}`))
	req.Header.Set(RoleHeader, roleExtension)
	rec := httptest.NewRecorder()
	ft.s.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("extension write: %d %s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/sessions/mac/s1/presentation", strings.NewReader(`{"display_name":"Named","tags":["one","two"]}`))
	rec = httptest.NewRecorder()
	ft.s.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("write: %d %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Session proto.Session `json:"session"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Session.Title != "Named" || out.Session.DisplayName() != "Named" || len(out.Session.Tags()) != 2 {
		t.Fatalf("response: %+v", out.Session)
	}

	req = httptest.NewRequest(http.MethodPut, "/api/v1/sessions/mac/s1/presentation", strings.NewReader(`{}`))
	rec = httptest.NewRecorder()
	ft.s.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty update: %d %s", rec.Code, rec.Body.String())
	}
}
