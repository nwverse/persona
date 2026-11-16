package assistant

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

func TestAgentsListDoneAndMutedUseTheSnapshotCategories(t *testing.T) {
	f := newFakeHub()
	now := time.Now()
	f.setSessions("box",
		proto.Session{SessionID: "done-recent", Title: "最近完成", State: proto.StateIdle, UpdatedAt: now.Add(-time.Hour),
			Work: &proto.WorkState{Phase: proto.WorkDone, Line: "修复并验证完成"}},
		proto.Session{SessionID: "done-old", Title: "一天前完成", State: proto.StateIdle, UpdatedAt: now.Add(-25 * time.Hour),
			Work: &proto.WorkState{Phase: proto.WorkDone, Line: "旧结论"}},
		proto.Session{SessionID: "done-muted", Title: "屏蔽的完成", State: proto.StateIdle, UpdatedAt: now,
			Work: &proto.WorkState{Phase: proto.WorkDone, Line: "屏蔽的结论"}},
		proto.Session{SessionID: "muted-need", Title: "屏蔽的提问", State: proto.StateNeedsYou, UpdatedAt: now},
		proto.Session{SessionID: "muted-idle", Title: "屏蔽的普通空闲", State: proto.StateIdle, UpdatedAt: now},
		proto.Session{SessionID: "plain-idle", Title: "普通空闲", State: proto.StateIdle, UpdatedAt: now},
		proto.Session{SessionID: "still-working", Title: "还在工作", State: proto.StateWorking, UpdatedAt: now,
			Work: &proto.WorkState{Phase: proto.WorkWorking}})
	f.muted = map[string][]string{"box": {"done-muted", "muted-need", "muted-idle"}}
	s := newStorelessService(t, f)

	done := s.describeAgents(context.Background(), "done", "", "")
	if !strings.Contains(done, "session_id=done-recent") || !strings.Contains(done, "work=done") ||
		!strings.Contains(done, "修复并验证完成") {
		t.Fatalf("the recent completion and its conclusion are not queryable:\n%s", done)
	}
	for _, unwanted := range []string{"done-old", "done-muted", "muted-need", "muted-idle", "plain-idle", "still-working"} {
		if strings.Contains(done, unwanted) {
			t.Errorf("state=done included %s:\n%s", unwanted, done)
		}
	}

	muted := s.describeAgents(context.Background(), "muted", "", "")
	for _, want := range []string{"session_id=done-muted", "session_id=muted-need", " · muted", "屏蔽的结论"} {
		if !strings.Contains(muted, want) {
			t.Errorf("state=muted lacks %q:\n%s", want, muted)
		}
	}
	for _, unwanted := range []string{"done-recent", "done-old", "muted-idle", "plain-idle", "still-working"} {
		if strings.Contains(muted, unwanted) {
			t.Errorf("state=muted included %s:\n%s", unwanted, muted)
		}
	}
}
