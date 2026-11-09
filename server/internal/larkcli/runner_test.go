package larkcli

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fakeConfig(t *testing.T, scenario string) Config {
	t.Helper()
	bin, err := filepath.Abs(filepath.Join("testdata", "fake-lark-cli.sh"))
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("LARKCLI_FAKE_SCENARIO", scenario)
	return Config{Bin: bin, Profile: "example", Timeout: time.Second}
}

func TestRunnerSuccessJSONAndProfile(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "args.log")
	t.Setenv("LARKCLI_FAKE_LOG", logPath)
	r := New(fakeConfig(t, "success"))
	ctx := context.Background()

	auth, err := r.AuthStatus(ctx)
	if err != nil || !auth.LoggedIn || auth.UserName != "Example User" || auth.OpenID != "ou_example_user" {
		t.Fatalf("auth = %+v, err = %v", auth, err)
	}
	if got := auth.GrantedScopes; len(got) != 2 || got[0] != "im:chat:read" || got[1] != "im:message:readonly" {
		t.Fatalf("scopes = %v", got)
	}
	chats, err := r.ListChats(ctx, 10)
	if err != nil || len(chats.Chats) != 2 || chats.Chats[0].ChatID != "oc_example_group" {
		t.Fatalf("chats = %+v, err = %v", chats, err)
	}
	atMe, err := r.ListChatsAtMe(ctx, 10)
	if err != nil || len(atMe.Chats) != 1 || atMe.Chats[0].ChatID != "oc_example_group" {
		t.Fatalf("at-me chats = %+v, err = %v", atMe, err)
	}
	messages, err := r.ListMessages(ctx, "oc_example_group", "", "", 10)
	if err != nil || len(messages.Messages) != 2 || messages.Messages[0].MessageID != "om_example_older" {
		t.Fatalf("messages = %+v, err = %v", messages, err)
	}
	search, err := r.SearchMessages(ctx, "example", SearchOptions{})
	if err != nil || len(search.Messages) != 1 || search.Messages[0].ChatName != "Example Group" {
		t.Fatalf("search = %+v, err = %v", search, err)
	}
	if err := r.CheckVersion(ctx); err != nil {
		t.Fatal(err)
	}
	log, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(log)
	if !strings.Contains(text, "--profile example") || !strings.Contains(text, "--as user --format json") {
		t.Fatalf("profile or user JSON flags missing from %q", text)
	}
}

func TestAuthStatusStates(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		loggedIn bool
		expired  bool
	}{
		{"not logged in", "not_logged_in", false, false},
		{"expired", "expired", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			auth, err := New(fakeConfig(t, tc.scenario)).AuthStatus(context.Background())
			if err != nil || auth.LoggedIn != tc.loggedIn || auth.Expired != tc.expired {
				t.Fatalf("auth = %+v, err = %v", auth, err)
			}
		})
	}
}

func TestErrorCategories(t *testing.T) {
	for _, tc := range []struct {
		name     string
		scenario string
		want     ErrorKind
		call     func(*Runner) error
	}{
		{"missing scope", "missing_scope", KindMissingPermission, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"rate limited", "rate_limited", KindRateLimited, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"non-zero", "nonzero", KindOther, func(r *Runner) error {
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"timeout", "timeout", KindTimeout, func(r *Runner) error {
			r.cfg.Timeout = 20 * time.Millisecond
			_, err := r.SearchMessages(context.Background(), "example", SearchOptions{})
			return err
		}},
		{"old version", "old", KindUnsupportedVersion, func(r *Runner) error { return r.CheckVersion(context.Background()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call(New(fakeConfig(t, tc.scenario)))
			if KindOf(err) != tc.want {
				t.Fatalf("kind = %q, err = %v", KindOf(err), err)
			}
			if tc.want == KindMissingPermission {
				var e *Error
				if !errors.As(err, &e) || len(e.MissingScopes) != 1 || e.MissingScopes[0] != "im:message:readonly" {
					t.Fatalf("missing scopes = %+v", err)
				}
			}
		})
	}
}

func TestStderrIsNotReturned(t *testing.T) {
	_, err := New(fakeConfig(t, "nonzero")).SearchMessages(context.Background(), "example", SearchOptions{})
	if strings.Contains(err.Error(), "message-body-example") {
		t.Fatalf("raw stderr leaked: %v", err)
	}
}

func TestNotInstalled(t *testing.T) {
	r := New(Config{Bin: filepath.Join(t.TempDir(), "missing-lark-cli")})
	if _, err := r.Version(context.Background()); KindOf(err) != KindNotInstalled {
		t.Fatalf("kind = %q, err = %v", KindOf(err), err)
	}
}

func TestContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := New(fakeConfig(t, "timeout")).SearchMessages(ctx, "example", SearchOptions{})
	if KindOf(err) != KindTimeout || !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, kind = %q", err, KindOf(err))
	}
}

func TestRecordedFixtures(t *testing.T) {
	for _, name := range []string{"auth_status.json", "auth_not_logged_in.json", "auth_expired.json", "auth_missing_scope.json", "chats.json", "messages.json", "search.json", "error_missing_scope.json", "error_rate_limited.json"} {
		raw, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(name, "auth_") && name != "error_missing_scope.json" {
			var v authStatusWire
			if err := jsonUnmarshal(raw, &v); err != nil {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if name == "chats.json" {
			var v ChatList
			if err := decodeData(raw, &v); err != nil || len(v.Chats) == 0 {
				t.Errorf("%s: %v", name, err)
			}
			continue
		}
		if name == "error_missing_scope.json" || name == "error_rate_limited.json" {
			want := KindMissingPermission
			if name == "error_rate_limited.json" {
				want = KindRateLimited
			}
			if got := classifyFailure(commandResult{stdout: raw}); got == nil || got.Kind != want {
				t.Errorf("%s: got %#v", name, got)
			}
			continue
		}
		var v MessagePage
		if err := decodeData(raw, &v); err != nil || len(v.Messages) == 0 {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Kept as a tiny indirection so the fixture contract test cannot accidentally
// accept a non-JSON sample through a helper with a different decoder.
func jsonUnmarshal(raw []byte, out any) error { return json.Unmarshal(raw, out) }
