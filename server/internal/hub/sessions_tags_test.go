package hub

import (
	"net/url"
	"slices"
	"testing"

	"github.com/nwverse/persona/server/internal/proto"
	"github.com/nwverse/persona/server/internal/sessionlist"
)

func TestSessionsTagFiltersCountsAndSummaries(t *testing.T) {
	h := newTestHub(t)
	m := newMachine(h, MachineConfig{ID: "demo"})
	h.machines, h.byID["demo"] = []*Machine{m}, m
	newRow := func(id, state string, tags ...string) proto.Session {
		row := proto.Session{MachineID: "demo", SessionID: id, Title: id, Cwd: "/" + id, State: state, Status: proto.StatusIdle}
		row.SetPresentation("", tags)
		return row
	}
	m.sessions = []proto.Session{
		newRow("a", proto.StateWorking, "review"),
		newRow("b", proto.StateIdle, "release", "review"),
		newRow("c", proto.StateWorking),
		newRow("d", proto.StateIdle, "Release"),
	}
	m.sessions[3].Archived = true

	page := runtimePage(t, h, "summary=1&tag=review&tag=release")
	if len(page.Sessions) != 2 || !slices.Contains([]string{page.Sessions[0].SessionID, page.Sessions[1].SessionID}, "a") ||
		!slices.Contains([]string{page.Sessions[0].SessionID, page.Sessions[1].SessionID}, "b") {
		t.Fatalf("OR tag page: %+v", page.Sessions)
	}
	for _, row := range page.Sessions {
		if len(row.Tags()) == 0 {
			t.Fatalf("summary dropped tags: %+v", row)
		}
	}
	page = runtimePage(t, h, "summary=1&tag=review&state=working")
	if len(page.Sessions) != 1 || page.Sessions[0].SessionID != "a" {
		t.Fatalf("tag and state: %+v", page.Sessions)
	}
	page = runtimePage(t, h, "summary=1&q="+url.QueryEscape("review"))
	if len(page.Sessions) != 2 {
		t.Fatalf("tag search: %+v", page.Sessions)
	}
	counts, err := h.sessionListIndex().Counts(sessionlist.Options{Tags: []string{"review"}})
	if err != nil {
		t.Fatal(err)
	}
	if counts.Total != 2 || counts.Tags["review"] != 2 || counts.Tags["release"] != 1 || counts.State[proto.StateWorking] != 1 {
		t.Fatalf("tag counts: %+v", counts)
	}
	if !slices.Contains(h.features(), featureSessionsTags) || slices.Contains(proto.Features, featureSessionsTags) {
		t.Fatal("sessions_tags must be a hub-advertised feature")
	}
}
