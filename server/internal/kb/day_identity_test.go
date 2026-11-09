package kb

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDayIdentitySurvivesRezoneAndReopen(t *testing.T) {
	s, e := replayStore(t)
	if _, err := s.Rezone(time.UTC); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(24 * time.Hour).Add(-23*time.Hour + 12*time.Second)
	ingestMessages(t, e, "example", "thread", 2,
		SourceMessage{ID: "sent-ref", At: at, Text: "Synthetic text\n  continued", Mine: true},
		SourceMessage{ID: "other-ref", At: at, Text: "Different text", SenderName: "Example"})
	check := func(e *Engine, day string, zone *time.Location) {
		t.Helper()
		msgs, _, _, identity, err := e.DayWithIdentity("example-thread", day)
		if err != nil || len(msgs) != 2 || identity.Chat.SourceID != "thread" || identity.Zone.String() != zone.String() {
			t.Fatalf("day: messages=%+v identity=%+v error=%v", msgs, identity, err)
		}
		first, other := identity.Messages[msgs[0].N], identity.Messages[msgs[1].N]
		if first.Ref != "sent-ref" || !first.At.Equal(at) || !first.Mine || first.Ambiguous ||
			other.Ref != "other-ref" || other.Mine || other.Ambiguous {
			t.Fatalf("identities: first=%+v other=%+v", first, other)
		}
	}
	check(e, at.Format("2006-01-02"), time.UTC)
	zone, err := time.LoadLocation("Etc/GMT+8")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Rezone(zone); err != nil {
		t.Fatal(err)
	}
	if at.In(zone).Format("2006-01-02") == at.Format("2006-01-02") {
		t.Fatal("fixture did not cross a day boundary")
	}
	check(e, at.In(zone).Format("2006-01-02"), zone)
	readOnly, err := OpenReadOnly(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	check(&Engine{Store: readOnly}, at.In(zone).Format("2006-01-02"), zone)
}

func TestDayIdentityRejectsAmbiguityAndLegacyTimestampGuesses(t *testing.T) {
	s, e := replayStore(t)
	year, month, dayOfMonth := time.Now().In(s.Zone()).Date()
	at := time.Date(year, month, dayOfMonth-1, 12, 0, 0, 0, s.Zone())
	ingestMessages(t, e, "example", "thread", 4,
		SourceMessage{ID: "same-one", At: at, Text: "Same text", Mine: true},
		SourceMessage{ID: "same-two", At: at.Add(time.Second), Text: "Same text", Mine: true},
		SourceMessage{ID: "legacy", At: at.Add(time.Minute), Text: "Legacy text", Mine: true},
		SourceMessage{ID: "edited", At: at.Add(2 * time.Minute), Text: "Original text", Mine: true})
	r, err := s.receipts("example", "thread", "example-thread")
	if err != nil {
		t.Fatal(err)
	}
	delete(r.Lines, "legacy") // Old receipts know only the timestamp.
	if err := writeJSON(s.path(KindChat, "example-thread", ingestReceipts), r); err != nil {
		t.Fatal(err)
	}
	day := at.In(s.Zone()).Format("2006-01-02")
	path := s.path(KindChat, "example-thread", "messages", day+".md")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.ReplaceAll(string(raw), "Original text", "Edited text")), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, _, _, identity, err := e.DayWithIdentity("example-thread", day)
	if err != nil || len(msgs) != 4 {
		t.Fatalf("day: %v, %v", msgs, err)
	}
	for i, m := range msgs {
		got := identity.Messages[m.N]
		if got.Ref != "" || got.Ambiguous != (i < 2) {
			t.Errorf("message %d guessed an identity: %+v", i, got)
		}
	}
	// Removing one indistinguishable line still leaves two possible receipts.
	lines := strings.Split(string(raw), "\n")
	lines = append(lines[:msgs[0].N-1], lines[msgs[0].N:]...)
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatal(err)
	}
	msgs, _, _, identity, err = e.DayWithIdentity("example-thread", day)
	if err != nil || !identity.Messages[msgs[0].N].Ambiguous {
		t.Fatalf("duplicate receipt: %+v, %v", identity, err)
	}
}

func TestDayIdentityFailsClosedOnIncompleteOrUnreadableReceipts(t *testing.T) {
	for _, name := range []string{ingestJournal, ingestReceipts, filepath.Join("messages", zoneFile)} {
		t.Run(name, func(t *testing.T) {
			s, e := replayStore(t)
			at := time.Now().Add(-time.Hour)
			ingestMessages(t, e, "example", "thread", 1, SourceMessage{ID: "ref", At: at, Text: "Synthetic text", Mine: true})
			if err := os.WriteFile(s.path(KindChat, "example-thread", name), []byte("broken"), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, _, _, _, err := e.DayWithIdentity("example-thread", at.In(s.Zone()).Format("2006-01-02")); err == nil {
				t.Fatal("corrupt or incomplete archive returned identity evidence")
			}
		})
	}
}
