package larkcli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
	adapter "github.com/nwverse/persona/server/internal/larkcli"
)

func docTestConnector(t *testing.T, scenario string) *Connector {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("..", "..", "larkcli", "testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	t.Setenv("LARKCLI_FAKE_PHASE", "initial")
	now := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	return &Connector{
		runner: adapter.New(adapter.Config{Bin: bin, Profile: "example", Timeout: time.Second, Now: func() time.Time { return now }}),
		docs:   newDocumentInbox(),
		now:    func() time.Time { return now },
	}
}

func TestDocumentCommentsMentionParticipationCursorAndDedup(t *testing.T) {
	c := docTestConnector(t, "comments_reply")
	link := "https://example.feishu.cn/docx/doc_example"
	if err := c.SetOptions(json.RawMessage(`{"documents":["` + link + `"]}`)); err != nil {
		t.Fatal(err)
	}
	if !c.pollDocuments(context.Background()) {
		t.Fatal("first comment round did not move")
	}
	things, err := c.Collect(context.Background())
	if err != nil || len(things) != 1 {
		t.Fatalf("things = %+v, err = %v", things, err)
	}
	first := things[0]
	if first.Kind != "doc" || first.Title != "Example design document" || !strings.Contains(first.Preview, "please review") {
		t.Fatalf("first thing = %+v", first)
	}
	if !strings.Contains(first.URL, "comment_id=comment_example") || first.Extra == nil || len(first.Extra.Doc) == 0 {
		t.Fatalf("comment link/block missing: %+v", first)
	}
	if c.pollDocuments(context.Background()) {
		t.Fatal("same comments were collected twice")
	}
	things, _ = c.Collect(context.Background())
	if len(things) != 1 || things[0].Last != first.Last {
		t.Fatalf("dedup changed the item: %+v", things)
	}

	t.Setenv("LARKCLI_FAKE_PHASE", "reply")
	if !c.pollDocuments(context.Background()) {
		t.Fatal("new reply did not move the item")
	}
	things, _ = c.Collect(context.Background())
	if len(things) != 1 || things[0].Thread != first.Thread || things[0].Last == first.Last || !strings.Contains(things[0].Preview, "missing step") {
		t.Fatalf("reply did not update one thread: %+v", things)
	}
}

func TestDocumentCommentsUnwatchedAndResolvedAreSkipped(t *testing.T) {
	c := docTestConnector(t, "comments")
	if c.pollDocuments(context.Background()) {
		t.Fatal("default-empty watch list polled a document")
	}
	things, _ := c.Collect(context.Background())
	if len(things) != 0 {
		t.Fatalf("unwatched things = %+v", things)
	}
	if err := c.SetOptions(json.RawMessage(`{"documents":["https://example.feishu.cn/docx/doc_example"]}`)); err != nil {
		t.Fatal(err)
	}
	if !c.pollDocuments(context.Background()) {
		t.Fatal("mention was not collected")
	}
	things, _ = c.Collect(context.Background())
	if len(things) != 1 || strings.Contains(things[0].Thread, "comment_solved") {
		t.Fatalf("resolved or unexpected comments = %+v", things)
	}
}

func TestUnreadableDocumentWarningIsPermanentForTheRound(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "fake.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	c := docTestConnector(t, "comments_unreadable")
	if err := c.SetOptions(json.RawMessage(`{"documents":["https://example.feishu.cn/docx/doc_example"]}`)); err != nil {
		t.Fatal(err)
	}
	if c.pollDocuments(context.Background()) {
		t.Fatal("unreadable document produced an item")
	}
	if c.pollDocuments(context.Background()) {
		t.Fatal("unreadable document produced an item on retry")
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "+list-comments"); got != 1 {
		t.Fatalf("unreadable document was retried %d times: %s", got, log)
	}
	if got := strings.Count(string(log), "+inspect"); got != 1 {
		t.Fatalf("unreadable document metadata was retried %d times: %s", got, log)
	}
	st := c.Status(context.Background())
	if st.State != connector.StateOK || !strings.Contains(st.Error, "Example design document") {
		t.Fatalf("settings warning = %+v", st)
	}
}

func TestDocumentCommentRateLimitBackoff(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "fake.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	c := docTestConnector(t, "comments_rate_limited")
	if err := c.SetOptions(json.RawMessage(`{"documents":["https://example.feishu.cn/docx/doc_example"]}`)); err != nil {
		t.Fatal(err)
	}
	c.pollDocuments(context.Background())
	c.pollDocuments(context.Background())
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(log), "+list-comments"); got != 1 {
		t.Fatalf("rate-limited document was retried immediately %d times: %s", got, log)
	}
}

func TestWikiSpaceExpansionIsBoundedAndOptionsRoundTrip(t *testing.T) {
	c := docTestConnector(t, "success")
	if err := c.SetOptions(json.RawMessage(`{"documents":[{"url":"https://example.feishu.cn/docx/doc_example"}],"wiki_spaces":["Example space"]}`)); err != nil {
		t.Fatal(err)
	}
	var options DocumentOptions
	if err := json.Unmarshal(c.Options(), &options); err != nil {
		t.Fatal(err)
	}
	if len(options.Documents) != 1 || len(options.WikiSpaces) != 1 || options.WikiSpaces[0] != "Example space" {
		t.Fatalf("options round trip = %+v", options)
	}
	if !c.pollDocuments(context.Background()) {
		t.Fatal("wiki document was not collected")
	}
	things, _ := c.Collect(context.Background())
	if len(things) != 2 {
		t.Fatalf("wiki expansion = %+v", things)
	}
	for _, thing := range things {
		if thing.Kind != "doc" || thing.Title == "" {
			t.Fatalf("bad wiki thing = %+v", thing)
		}
	}
}
