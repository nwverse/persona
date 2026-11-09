package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/connector"
)

// healthyGitHub answers /user with a nearly full primary quota (4900 of 5000 left).
func healthyGitHub(t *testing.T) *Connector {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Remaining", "4900")
		w.Header().Set("X-RateLimit-Reset", "9999999999")
		_, _ = w.Write([]byte(`{"login":"alice"}`))
	}))
	t.Cleanup(srv.Close)
	c := New(Config{Auth: AuthConfig{FromMachine: "mac"}, APIRoot: srv.URL}, "Mac", (&machine{tokens: []string{oldToken}}).fetch)
	c.check(context.Background())
	return c
}

func retryAfter(v string) http.Header {
	h := http.Header{}
	h.Set("Retry-After", v)
	return h
}

var secondaryLimit = errors.New("GitHub GET /x: 429 secondary rate limit")

// A Retry-After pause holds until it ends, whatever good answers come in between.
func TestPauseOutlastsGoodAnswers(t *testing.T) {
	c := healthyGitHub(t)
	c.notePause(retryAfter("600"), secondaryLimit)
	if !c.ratesLow() {
		t.Fatal("not paused after a secondary rate limit")
	}
	var u struct{ Login string }
	if _, err := c.Get(context.Background(), "/user", &u); err != nil {
		t.Fatal(err)
	}
	if !c.ratesLow() {
		t.Fatal("a good answer lifted the pause")
	}
	if st := c.Status(context.Background()); st.State != connector.StateRateLimited || !strings.Contains(st.Error, "约 10 分钟") {
		t.Fatalf("%+v", st)
	}
	if reset := c.PRChecksRateReset(); reset.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("checks resume at %v", reset)
	}

	// A shorter Retry-After later on does not shorten the pause.
	c.notePause(retryAfter("30"), secondaryLimit)
	if reset := c.PRChecksRateReset(); reset.Before(time.Now().Add(9 * time.Minute)) {
		t.Fatalf("a shorter Retry-After moved the end to %v", reset)
	}

	// Over, the pause no longer holds anything back.
	c.mu.Lock()
	c.pausedUntil = time.Now().Add(-time.Second)
	c.mu.Unlock()
	if c.ratesLow() || !c.PRChecksRateReset().IsZero() {
		t.Fatal("still paused after the pause ended")
	}
	if st := c.Status(context.Background()); st.State != connector.StateOK {
		t.Fatalf("%+v", st)
	}
}

// An error that is not a rate limit does not pause.
func TestPauseOnlyForRateLimits(t *testing.T) {
	c := healthyGitHub(t)
	c.notePause(http.Header{}, errors.New("GitHub GET /x: 403 Resource not accessible by integration"))
	c.notePause(http.Header{}, errors.New("GitHub GET /x: 500 boom"))
	if c.ratesLow() {
		t.Fatal("paused without a rate limit")
	}
}

// The primary quota works as before: a low quota saves requests, a good one ends that, and the later of a low
// quota's reset and a pause's end is when requests resume.
func TestPrimaryQuotaBesidePause(t *testing.T) {
	c := healthyGitHub(t)
	quota := func(left string, reset time.Time) {
		h := http.Header{}
		h.Set("X-RateLimit-Limit", "5000")
		h.Set("X-RateLimit-Remaining", left)
		h.Set("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		c.noteRate(h)
	}
	reset := time.Now().Add(time.Hour)
	quota("100", reset)
	if !c.ratesLow() {
		t.Fatal("100 of 5000 left is not low")
	}
	quota("4900", reset)
	if c.ratesLow() {
		t.Fatal("4900 of 5000 left is still low")
	}

	quota("100", reset)
	c.notePause(retryAfter("60"), secondaryLimit)
	if got := c.PRChecksRateReset(); got.Unix() != reset.Unix() {
		t.Fatalf("resume at %v, not at the quota's reset %v", got, reset)
	}
	if st := c.Status(context.Background()); st.State != connector.StateRateLimited || !strings.Contains(st.Error, "约 60 分钟") {
		t.Fatalf("%+v", st)
	}
	quota("4900", reset)
	if got := c.PRChecksRateReset(); got.Before(time.Now().Add(50*time.Second)) || got.After(time.Now().Add(time.Minute)) {
		t.Fatalf("resume at %v, not at the pause's end", got)
	}
}
