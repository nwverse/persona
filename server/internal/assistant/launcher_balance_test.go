package assistant

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nwverse/persona/server/internal/proto"
)

// Official DeepSeek launchers report a prepaid balance at the provider's own origin (#11825). The manager reads it
// with the launcher's sealed key and hands on an amount and currency, never the key.
func TestLauncherBalancesReadsTheOfficialDeepSeekBalance(t *testing.T) {
	s, mux := launcherTestService(t)
	base := "https://api.deepseek.com/anthropic"
	if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
		`{"name":"deepseek","base_url":"`+base+`","model":"deepseek-v4-pro","api_key":"`+fakeKey+`"}`); code != 200 {
		t.Fatal(code, raw)
	}
	var requests []*http.Request
	var bodies []string
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		requests = append(requests, r.Clone(context.Background()))
		return probeResponse(r, 200, `{"is_available":true,"balance_infos":[{"currency":"CNY","total_balance":"12.34","granted_balance":"0.00","topped_up_balance":"12.34"}]}`), nil
	})
	accounts, known := s.LauncherBalances(context.Background())
	if !known || len(accounts) != 1 {
		t.Fatalf("known=%v accounts=%+v", known, accounts)
	}
	a := accounts[0]
	if a.Provider != "deepseek" || a.Name != "deepseek" || a.Problem != "" || a.Balance == nil {
		t.Fatalf("account: %+v", a)
	}
	if a.Balance.Amount != "12.34" || a.Balance.Currency != "CNY" {
		t.Fatalf("balance: %+v", a.Balance)
	}
	if len(requests) != 1 {
		t.Fatalf("requests: %d", len(requests))
	}
	r := requests[0]
	if r.Method != http.MethodGet || r.URL.String() != "https://api.deepseek.com/user/balance" {
		t.Fatalf("request: %s %s", r.Method, r.URL)
	}
	if r.Header.Get("Authorization") != "Bearer "+fakeKey || r.Header.Get("x-api-key") != fakeKey {
		t.Fatal("the launcher's key was not sent to its own provider")
	}
	raw, _ := json.Marshal(accounts)
	bodies = append(bodies, string(raw))
	for _, body := range bodies {
		if strings.Contains(body, fakeKey) {
			t.Fatalf("the key leaked into a sample: %s", body)
		}
	}
}

// A genuine zero is a balance the sidebar can show, not the absence of one.
func TestLauncherBalancesKeepsAGenuineZero(t *testing.T) {
	s, mux := launcherTestService(t)
	if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
		`{"name":"deepseek","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key":"`+fakeKey+`"}`); code != 200 {
		t.Fatal(code, raw)
	}
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		return probeResponse(r, 200, `{"is_available":false,"balance_infos":[{"currency":"cny","total_balance":"0.00"}]}`), nil
	})
	accounts, _ := s.LauncherBalances(context.Background())
	if len(accounts) != 1 || accounts[0].Balance == nil || accounts[0].Balance.Amount != "0.00" || accounts[0].Balance.Currency != "CNY" || accounts[0].Problem != "" {
		t.Fatalf("zero balance: %+v", accounts)
	}
}

// A failed read is a problem code, never a made-up zero.
func TestLauncherBalancesReportsFailures(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		netErr bool
		want   string
	}{
		{name: "refused key", status: http.StatusUnauthorized, body: `{"error":{"message":"bad key"}}`, want: "rejected"},
		{name: "forbidden key", status: http.StatusForbidden, body: `{}`, want: "rejected"},
		{name: "server error", status: http.StatusInternalServerError, body: `{}`, want: "failed"},
		{name: "redirect", status: http.StatusFound, body: ``, want: "failed"},
		{name: "not json", status: http.StatusOK, body: `not json`, want: "failed"},
		{name: "no amount", status: http.StatusOK, body: `{"balance_infos":[]}`, want: "failed"},
		{name: "bad amount", status: http.StatusOK, body: `{"balance_infos":[{"currency":"CNY","total_balance":"twelve"}]}`, want: "failed"},
		{name: "unreachable", netErr: true, want: "unreachable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, mux := launcherTestService(t)
			if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
				`{"name":"deepseek","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key":"`+fakeKey+`"}`); code != 200 {
				t.Fatal(code, raw)
			}
			interceptProbe(t, func(r *http.Request) (*http.Response, error) {
				if tc.netErr {
					return nil, io.ErrUnexpectedEOF
				}
				return probeResponse(r, tc.status, tc.body), nil
			})
			accounts, known := s.LauncherBalances(context.Background())
			if !known || len(accounts) != 1 || accounts[0].Problem != tc.want || accounts[0].Balance != nil {
				t.Fatalf("%s: known=%v %+v", tc.name, known, accounts)
			}
		})
	}
}

// Only the provider's own origin is asked: a gateway or a lookalike host never receives the launcher's key here.
func TestLauncherBalancesIgnoresOtherOrigins(t *testing.T) {
	for _, base := range []string{
		"https://gateway.example.com/anthropic",
		"https://api.deepseek.com.example.com/anthropic",
		"https://api.deepseek.com:8443/anthropic",
		"http://api.deepseek.com/anthropic",
		"https://api.deepseek.com/team/anthropic",
	} {
		s, mux := launcherTestService(t)
		if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
			`{"name":"gw","base_url":"`+base+`","model":"deepseek-v4-pro","api_key":"`+fakeKey+`"}`); code != 200 {
			t.Fatal(code, raw)
		}
		asked := false
		interceptProbe(t, func(r *http.Request) (*http.Response, error) {
			asked = true
			return probeResponse(r, 200, `{}`), nil
		})
		if accounts, known := s.LauncherBalances(context.Background()); !known || len(accounts) != 0 || asked {
			t.Fatalf("%s: known=%v accounts=%+v asked=%v", base, known, accounts, asked)
		}
	}
}

// A launcher with no key of its own may use the key one of its computers keeps (#11825: the launcher's credential
// mechanism, not a second one).
func TestLauncherBalancesUsesAComputersKey(t *testing.T) {
	s, mux := launcherTestService(t)
	if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
		`{"name":"deepseek","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro"}`); code != 200 {
		t.Fatal(code, raw)
	}
	sealed, err := s.sealKey(fakeKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.putSetting(context.Background(), launchersMachineKeysKey, []machineKey{{Launcher: "deepseek", Machine: "m1", KeySealed: sealed}}); err != nil {
		t.Fatal(err)
	}
	var key string
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		key = r.Header.Get("Authorization")
		return probeResponse(r, 200, `{"balance_infos":[{"currency":"CNY","total_balance":"3.00"}]}`), nil
	})
	accounts, _ := s.LauncherBalances(context.Background())
	if len(accounts) != 1 || accounts[0].Balance == nil || accounts[0].Balance.Amount != "3.00" || key != "Bearer "+fakeKey {
		t.Fatalf("accounts=%+v key=%q", accounts, key)
	}
}

// Two launchers that hold one key are one account: one row, under the first launcher's name.
func TestLauncherBalancesDeduplicatesOneKey(t *testing.T) {
	s, mux := launcherTestService(t)
	for _, name := range []string{"deepseek", "deepseek-flash"} {
		if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
			`{"name":"`+name+`","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key":"`+fakeKey+`"}`); code != 200 {
			t.Fatal(code, raw)
		}
	}
	requests := 0
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		requests++
		return probeResponse(r, 200, `{"balance_infos":[{"currency":"CNY","total_balance":"1.00"}]}`), nil
	})
	accounts, _ := s.LauncherBalances(context.Background())
	if len(accounts) != 1 || requests != 1 {
		t.Fatalf("accounts=%+v requests=%d", accounts, requests)
	}
}

// A corrupt or unresolvable key on the first computer must not hide a usable key on the next (#11825).
func TestLauncherBalancesTriesTheNextComputersKey(t *testing.T) {
	s, mux := launcherTestService(t)
	if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
		`{"name":"deepseek","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro"}`); code != 200 {
		t.Fatal(code, raw)
	}
	sealed, err := s.sealKey(fakeKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.putSetting(context.Background(), launchersMachineKeysKey, []machineKey{
		{Launcher: "deepseek", Machine: "a-first", KeySealed: "not-a-sealed-value"},
		{Launcher: "deepseek", Machine: "b-second", KeySealed: sealed},
	}); err != nil {
		t.Fatal(err)
	}
	var key string
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		key = r.Header.Get("Authorization")
		return probeResponse(r, 200, `{"balance_infos":[{"currency":"CNY","total_balance":"3.00"}]}`), nil
	})
	accounts, known := s.LauncherBalances(context.Background())
	if !known || len(accounts) != 1 || accounts[0].Balance == nil || accounts[0].Balance.Amount != "3.00" || key != "Bearer "+fakeKey {
		t.Fatalf("known=%v accounts=%+v key=%q", known, accounts, key)
	}
}

// A key that cannot be opened fails before any request, so it is a local credential problem ("failed"), never the
// provider turning the key away ("rejected") (#11825).
func TestLauncherBalancesReportsAnUnopenableKeyAsFailed(t *testing.T) {
	s, _ := launcherTestService(t)
	putLaunchers(t, s, storedLauncher{KeySealed: "not-a-sealed-value",
		ManagedLauncher: proto.ManagedLauncher{Name: "deepseek", BaseURL: "https://api.deepseek.com/anthropic"}})
	asked := false
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		asked = true
		return probeResponse(r, 200, `{"balance_infos":[{"currency":"CNY","total_balance":"1.00"}]}`), nil
	})
	accounts, known := s.LauncherBalances(context.Background())
	if !known || len(accounts) != 1 || accounts[0].Problem != "failed" || accounts[0].Balance != nil || asked {
		t.Fatalf("known=%v accounts=%+v asked=%v", known, accounts, asked)
	}
}

// Two launchers sharing one broken key are one account still: one "balance unavailable" row, not two (#11825).
func TestLauncherBalancesReportsASharedBrokenKeyOnce(t *testing.T) {
	s, _ := launcherTestService(t)
	putLaunchers(t, s,
		storedLauncher{KeySealed: "not-a-sealed-value",
			ManagedLauncher: proto.ManagedLauncher{Name: "deepseek", BaseURL: "https://api.deepseek.com/anthropic"}},
		storedLauncher{KeySealed: "not-a-sealed-value",
			ManagedLauncher: proto.ManagedLauncher{Name: "deepseek-flash", BaseURL: "https://api.deepseek.com/anthropic"}},
	)
	accounts, known := s.LauncherBalances(context.Background())
	if !known || len(accounts) != 1 || accounts[0].Problem != "failed" || accounts[0].Balance != nil {
		t.Fatalf("known=%v accounts=%+v", known, accounts)
	}
}

// keyFailStore fails one setting's read, as a database error on that row only.
type keyFailStore struct {
	Store
	fail string
}

func (s keyFailStore) Setting(ctx context.Context, key string, out any) (bool, error) {
	if key == s.fail {
		return false, errors.New("database unavailable")
	}
	return s.Store.Setting(ctx, key, out)
}

// A machine-key store that cannot be read is not "every computer's key is gone": the caller keeps what it had
// instead of dropping the launchers that rely on such a key (#11825).
func TestLauncherBalancesKeepsSamplesWhenTheMachineKeyStoreCannotBeRead(t *testing.T) {
	ctx := context.Background()
	s, _, st := modelService(t)
	putLaunchers(t, s, storedLauncher{
		ManagedLauncher: proto.ManagedLauncher{Name: "deepseek", BaseURL: "https://api.deepseek.com/anthropic"}})
	sealed, err := s.sealKey(fakeKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.putSetting(ctx, launchersMachineKeysKey, []machineKey{{Launcher: "deepseek", Machine: "m1", KeySealed: sealed}}); err != nil {
		t.Fatal(err)
	}
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		return probeResponse(r, 200, `{"balance_infos":[{"currency":"CNY","total_balance":"3.00"}]}`), nil
	})
	if accounts, known := s.LauncherBalances(ctx); !known || len(accounts) != 1 || accounts[0].Balance == nil {
		t.Fatalf("first read: known=%v accounts=%+v", known, accounts)
	}

	s.store = func() Store { return keyFailStore{Store: st, fail: launchersMachineKeysKey} }
	if accounts, known := s.LauncherBalances(ctx); known || accounts != nil {
		t.Fatalf("an unreadable key store must not answer: known=%v accounts=%+v", known, accounts)
	}
}

// The balance read goes where the launcher's own calls go: through its proxy (honoring no_proxy) or through the
// computer it names, with the same no-redirect policy as a probe (#11825).
func TestBalanceClientFollowsTheLaunchersRoute(t *testing.T) {
	s, _ := launcherTestService(t)
	direct := s.balanceClient(storedLauncher{})
	if direct.Timeout != balanceTimeout || direct.CheckRedirect == nil || direct.Transport != nil {
		t.Fatalf("direct: %+v", direct)
	}
	proxied := s.balanceClient(storedLauncher{ManagedLauncher: proto.ManagedLauncher{
		Proxy: "http://proxy.example.com:8080", NoProxy: "localhost"}})
	tr, ok := proxied.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("proxied transport: %T", proxied.Transport)
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.deepseek.com/user/balance", nil)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := tr.Proxy(req); err != nil || u == nil || u.String() != "http://proxy.example.com:8080" {
		t.Fatalf("the provider's address must use the launcher's proxy: %v %v", u, err)
	}
	bypassed, err := http.NewRequest(http.MethodGet, "http://localhost:8080/user/balance", nil)
	if err != nil {
		t.Fatal(err)
	}
	if u, err := tr.Proxy(bypassed); err != nil || u != nil {
		t.Fatalf("no_proxy must exempt its addresses: %v %v", u, err)
	}
	via := s.balanceClient(storedLauncher{ViaMachine: "m1"})
	if v, ok := via.Transport.(viaNode); !ok || v.machine != "m1" || v.s != s {
		t.Fatalf("via machine: %#v", via.Transport)
	}
}

// Rotating what a secret reference (such as op://…) resolves to changes BalanceCredential even though the stored
// sealed reference bytes stay the same, so a subsequent failed read cannot keep showing the previous key's balance
// (#11825). Keys that fail to open still fall back to a sealed-material digest distinct from resolved keys.
func TestLauncherBalancesCredentialChangesWhenReferenceRotates(t *testing.T) {
	ctx := context.Background()
	s, mux := launcherTestService(t)
	const (
		ref  = "exv://Example/deepseek/key"
		key1 = "sk-deepseek-rotated-old-0001"
		key2 = "sk-deepseek-rotated-new-0002"
	)
	vault := &fakeLauncherSecrets{values: map[string]string{ref: key1}}
	s.refs = vault
	if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
		`{"name":"deepseek","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key_reference":"`+ref+`"}`); code != 200 {
		t.Fatal(code, raw)
	}
	storedBefore := s.storedLaunchers(ctx)
	if len(storedBefore) != 1 || storedBefore[0].KeySealed == "" {
		t.Fatalf("stored: %+v", storedBefore)
	}
	sealedBefore := storedBefore[0].KeySealed

	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		switch r.Header.Get("Authorization") {
		case "Bearer " + key1:
			return probeResponse(r, http.StatusOK, `{"balance_infos":[{"currency":"CNY","total_balance":"12.34"}]}`), nil
		case "Bearer " + key2:
			return probeResponse(r, http.StatusUnauthorized, `{"error":{"message":"bad key"}}`), nil
		default:
			return probeResponse(r, http.StatusInternalServerError, `{}`), nil
		}
	})

	first, known := s.LauncherBalances(ctx)
	if !known || len(first) != 1 || first[0].Balance == nil || first[0].Balance.Amount != "12.34" || first[0].Problem != "" {
		t.Fatalf("first read: known=%v %+v", known, first)
	}
	want1 := sha256.Sum256([]byte(key1))
	if first[0].BalanceCredential != hex.EncodeToString(want1[:]) {
		t.Fatalf("first credential=%q want=%q", first[0].BalanceCredential, hex.EncodeToString(want1[:]))
	}

	// Rotate the secret behind the same sealed reference without touching the stored launcher.
	vault.mu.Lock()
	vault.values[ref] = key2
	vault.mu.Unlock()
	if storedAfter := s.storedLaunchers(ctx); len(storedAfter) != 1 || storedAfter[0].KeySealed != sealedBefore {
		t.Fatalf("sealed reference should be unchanged: %+v", storedAfter)
	}

	second, known := s.LauncherBalances(ctx)
	if !known || len(second) != 1 || second[0].Balance != nil || second[0].Problem != "rejected" {
		t.Fatalf("rotated read: known=%v %+v", known, second)
	}
	want2 := sha256.Sum256([]byte(key2))
	if second[0].BalanceCredential != hex.EncodeToString(want2[:]) {
		t.Fatalf("rotated credential=%q want=%q", second[0].BalanceCredential, hex.EncodeToString(want2[:]))
	}
	if second[0].BalanceCredential == first[0].BalanceCredential {
		t.Fatalf("rotating a reference must change BalanceCredential: %q", second[0].BalanceCredential)
	}
	if strings.Contains(second[0].BalanceCredential, key1) || strings.Contains(second[0].BalanceCredential, key2) {
		t.Fatalf("BalanceCredential must never carry a key: %q", second[0].BalanceCredential)
	}

	// If the reference later fails to resolve, the fallback sealed digest is still distinct from resolved keys.
	vault.mu.Lock()
	delete(vault.values, ref)
	vault.mu.Unlock()
	broken, known := s.LauncherBalances(ctx)
	if !known || len(broken) != 1 || broken[0].Balance != nil || broken[0].Problem != "failed" {
		t.Fatalf("broken reference read: known=%v %+v", known, broken)
	}
	if broken[0].BalanceCredential != sealedCredential(sealedBefore) || broken[0].BalanceCredential == second[0].BalanceCredential {
		t.Fatalf("broken fallback credential=%q want=%q", broken[0].BalanceCredential, sealedCredential(sealedBefore))
	}
}

// Multiple slow launchers must not consume the whole poll budget sequentially and starve a later healthy launcher
// (#11825): deduplicated balance reads run concurrently with a bounded worker count.
func TestLauncherBalancesReadsConcurrentlySoSlowLaunchersDoNotStarveHealthyOnes(t *testing.T) {
	oldTimeout := balanceTimeout
	balanceTimeout = 200 * time.Millisecond
	t.Cleanup(func() { balanceTimeout = oldTimeout })

	s, mux := launcherTestService(t)
	for _, tc := range []struct{ name, key string }{
		{"slow-1", "sk-deepseek-slow-0001"},
		{"slow-2", "sk-deepseek-slow-0002"},
		{"healthy", "sk-deepseek-healthy-0003"},
	} {
		if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
			`{"name":"`+tc.name+`","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key":"`+tc.key+`"}`); code != 200 {
			t.Fatal(code, raw)
		}
	}

	var healthyAttempts atomic.Int32
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "Bearer sk-deepseek-healthy-0003" {
			healthyAttempts.Add(1)
			return probeResponse(r, http.StatusOK, `{"balance_infos":[{"currency":"CNY","total_balance":"42.00"}]}`), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	// Overall budget is shorter than 2 * balanceTimeout, so two slow reads in sequence would exhaust it before
	// healthy was ever attempted.
	ctx, cancel := context.WithTimeout(context.Background(), 350*time.Millisecond)
	defer cancel()
	accounts, known := s.LauncherBalances(ctx)
	if !known || len(accounts) != 3 {
		t.Fatalf("known=%v accounts=%+v", known, accounts)
	}
	if healthyAttempts.Load() != 1 {
		t.Fatalf("healthy launcher was not attempted: %d", healthyAttempts.Load())
	}
	if accounts[0].Name != "slow-1" || accounts[0].Problem != "unreachable" || accounts[0].Balance != nil || accounts[0].BalanceSkipped {
		t.Fatalf("slow-1: %+v", accounts[0])
	}
	if accounts[1].Name != "slow-2" || accounts[1].Problem != "unreachable" || accounts[1].Balance != nil || accounts[1].BalanceSkipped {
		t.Fatalf("slow-2: %+v", accounts[1])
	}
	if accounts[2].Name != "healthy" || accounts[2].Problem != "" || accounts[2].BalanceSkipped ||
		accounts[2].Balance == nil || accounts[2].Balance.Amount != "42.00" || accounts[2].Balance.Currency != "CNY" {
		t.Fatalf("healthy: %+v", accounts[2])
	}
}

// When the overall poll context expires before every launcher gets a full attempt, neither in-flight nor queued
// launchers are misreported as unreachable (#11825).
func TestLauncherBalancesDoesNotTurnContextExhaustionIntoUnreachable(t *testing.T) {
	oldTimeout, oldWorkers := balanceTimeout, balanceReadWorkers
	balanceTimeout = 2 * time.Second
	balanceReadWorkers = 2
	t.Cleanup(func() {
		balanceTimeout = oldTimeout
		balanceReadWorkers = oldWorkers
	})

	s, mux := launcherTestService(t)
	for _, tc := range []struct{ name, key string }{
		{"slow-1", "sk-deepseek-exhaust-0001"},
		{"slow-2", "sk-deepseek-exhaust-0002"},
		{"queued-3", "sk-deepseek-exhaust-0003"},
	} {
		if code, raw := launcherCall(t, mux, "POST", "/api/v1/launchers",
			`{"name":"`+tc.name+`","base_url":"https://api.deepseek.com/anthropic","model":"deepseek-v4-pro","api_key":"`+tc.key+`"}`); code != 200 {
			t.Fatal(code, raw)
		}
	}

	var queuedAttempts atomic.Int32
	interceptProbe(t, func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") == "Bearer sk-deepseek-exhaust-0003" {
			queuedAttempts.Add(1)
			return probeResponse(r, http.StatusOK, `{"balance_infos":[{"currency":"CNY","total_balance":"9.99"}]}`), nil
		}
		<-r.Context().Done()
		return nil, r.Context().Err()
	})

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	accounts, known := s.LauncherBalances(ctx)
	if !known || len(accounts) != 3 {
		t.Fatalf("known=%v accounts=%+v", known, accounts)
	}
	if queuedAttempts.Load() != 0 {
		t.Fatalf("queued-3 should not have been attempted after context expiry: %d", queuedAttempts.Load())
	}
	for _, a := range accounts {
		if !a.BalanceSkipped || a.Problem != "" || a.Balance != nil {
			t.Fatalf("context exhaustion must mark skipped, never unreachable: %+v", a)
		}
	}
}
