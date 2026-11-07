package agent

import (
	"context"
	"errors"
	"maps"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"
)

// The providers the tests keep accounts of, by their catalog IDs. Package
// agent names no provider of its own.
const (
	testAnthropic ProviderID = "anthropic"
	testOpenAI    ProviderID = "openai"
)

// fakeFlow is a LoginFlow whose steps each test sets.
type fakeFlow struct {
	login   func(ctx context.Context) (OAuthToken, error)
	refresh func(ctx context.Context, t OAuthToken) (OAuthToken, error)
}

func (f *fakeFlow) Browser(ctx context.Context, show func(LoginPrompt)) (OAuthToken, error) {
	show(LoginPrompt{URL: "https://login.test/authorize"})
	return f.login(ctx)
}

func (f *fakeFlow) Device(ctx context.Context, show func(LoginPrompt)) (OAuthToken, error) {
	show(LoginPrompt{URL: "https://login.test/device", Code: "ABCD-1234"})
	return f.login(ctx)
}

func (f *fakeFlow) Refresh(ctx context.Context, t OAuthToken) (OAuthToken, error) {
	return f.refresh(ctx, t)
}

// noRefresh returns a LoginFlow that fails the test when it refreshes.
func noRefresh(t *testing.T) *fakeFlow {
	return &fakeFlow{refresh: func(context.Context, OAuthToken) (OAuthToken, error) {
		t.Error("unexpected refresh")
		return OAuthToken{}, errors.ErrUnsupported
	}}
}

// newAccounts returns an Accounts over a fresh store, with key as the
// configured OpenAI key and flow as the OpenAI login.
func newAccounts(t *testing.T, key APIKey, flow LoginFlow) *Accounts {
	t.Helper()
	return NewAccounts(AccountsConfig{
		Store: AuthFile{Dir: t.TempDir()},
		Keys: func(p ProviderID) (APIKey, error) {
			if p == testOpenAI {
				return key, nil
			}
			return "", nil
		},
		Flows: map[ProviderID]LoginFlow{testOpenAI: flow},
	})
}

// store saves e as the OpenAI entry of s.
func store(t *testing.T, s *Accounts, e AuthEntry) {
	t.Helper()
	err := s.store.Update(t.Context(), testOpenAI, func(stored *AuthEntry) error {
		*stored = e
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

// saved returns the OpenAI entry of s.
func saved(t *testing.T, s *Accounts) AuthEntry {
	t.Helper()
	e, err := s.store.Load(testOpenAI)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func apiKeyAuth(key string) Authorization {
	return Authorization{Kind: CredentialAPIKey, Token: key}
}

func oauthAuth(access string) Authorization {
	return Authorization{Kind: CredentialOAuth, Token: access}
}

func TestAuthorize(t *testing.T) {
	later := time.Now().Add(time.Hour)
	two := map[AccountID]Credential{
		idWork: OAuthToken{Access: "work-token", Refresh: "r", Expiry: later},
		idHome: APIKey("sk-home"),
	}
	tests := []struct {
		name    string
		key     APIKey // configured key
		entry   AuthEntry
		want    Authorization
		wantErr error
	}{
		{name: "no account", wantErr: ErrNotAuthenticated},
		{name: "config key only", key: "sk-config", want: apiKeyAuth("sk-config")},
		{name: "active saved account", key: "sk-config", entry: AuthEntry{Active: idWork, Accounts: two}, want: oauthAuth("work-token")},
		{name: "active config account", key: "sk-config", entry: AuthEntry{Active: ConfigAccount, Accounts: two}, want: apiKeyAuth("sk-config")},
		{name: "none active prefers config", key: "sk-config", entry: AuthEntry{Accounts: two}, want: apiKeyAuth("sk-config")},
		{name: "none active takes first by name", entry: AuthEntry{Accounts: two}, want: apiKeyAuth("sk-home")},
		{name: "missing active account", entry: AuthEntry{Active: idGone, Accounts: two}, want: apiKeyAuth("sk-home")},
		{name: "config active without key", entry: AuthEntry{Active: ConfigAccount, Accounts: two}, want: apiKeyAuth("sk-home")},
		{
			name:  "oauth token without expiry",
			entry: AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: OAuthToken{Access: "tok"}}},
			want:  oauthAuth("tok"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAccounts(t, tt.key, noRefresh(t))
			if tt.entry.Accounts != nil || tt.entry.Active != "" {
				store(t, s, tt.entry)
			}
			got, err := s.For(testOpenAI).Authorize(t.Context(), nil)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authorize error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Authorize = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAuthorizeKeyError(t *testing.T) {
	errConfig := errors.New("bad config")
	s := NewAccounts(AccountsConfig{
		Store: AuthFile{Dir: t.TempDir()},
		Keys:  func(ProviderID) (APIKey, error) { return "", errConfig },
	})
	if _, err := s.Authorize(t.Context(), testOpenAI, nil); !errors.Is(err, errConfig) {
		t.Errorf("Authorize error = %v, want %v", err, errConfig)
	}
}

func TestAuthorizeRefresh(t *testing.T) {
	var got OAuthToken
	flow := &fakeFlow{refresh: func(_ context.Context, tok OAuthToken) (OAuthToken, error) {
		got = tok
		// The server keeps the old refresh token valid and sends none.
		return OAuthToken{Access: "new", Expiry: time.Now().Add(time.Hour)}, nil
	}}
	s := newAccounts(t, "", flow)
	other := OAuthToken{Access: "other", Refresh: "r9", Expiry: time.Now().Add(-time.Minute).Truncate(time.Second)}
	store(t, s, AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{
		idWork: OAuthToken{Access: "old", Refresh: "r1", Expiry: time.Now().Add(10 * time.Second)},
		idHome: other,
	}})

	a, err := s.Authorize(t.Context(), testOpenAI, nil)
	if err != nil {
		t.Fatal(err)
	}
	if a != oauthAuth("new") {
		t.Errorf("Authorize = %+v, want the refreshed token", a)
	}
	if got.Refresh != "r1" {
		t.Errorf("refreshed with %q, want r1", got.Refresh)
	}
	e := saved(t, s)
	if tok := e.Accounts[idWork].(OAuthToken); tok.Access != "new" || tok.Refresh != "r1" {
		t.Errorf("saved %+v, want access new and the kept refresh token r1", tok)
	}
	if !equalCredential(e.Accounts[idHome], other) {
		t.Error("refresh changed an inactive account")
	}
}

// TestAuthorizeRefreshOnce checks that concurrent callers who all find the
// token expiring refresh it once.
func TestAuthorizeRefreshOnce(t *testing.T) {
	var refreshes atomic.Int32
	flow := &fakeFlow{refresh: func(context.Context, OAuthToken) (OAuthToken, error) {
		refreshes.Add(1)
		time.Sleep(50 * time.Millisecond) // widen the window for a second refresh
		return OAuthToken{Access: "new", Refresh: "r2", Expiry: time.Now().Add(time.Hour)}, nil
	}}
	s := newAccounts(t, "", flow)
	store(t, s, AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{
		idWork: OAuthToken{Access: "old", Refresh: "r1", Expiry: time.Now().Add(-time.Minute)},
	}})

	tokens := concurrently(10, func() (Authorization, error) {
		return s.Authorize(t.Context(), testOpenAI, nil)
	}, t)
	if n := refreshes.Load(); n != 1 {
		t.Errorf("refreshed %d times, want 1", n)
	}
	if i := slices.IndexFunc(tokens, func(a Authorization) bool { return a != oauthAuth("new") }); i >= 0 {
		t.Errorf("caller %d got %+v, want the new token", i, tokens[i])
	}
}

// concurrently runs fn in n goroutines and returns their results.
func concurrently(n int, fn func() (Authorization, error), t *testing.T) []Authorization {
	t.Helper()
	results := make([]Authorization, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			a, err := fn()
			if err != nil {
				t.Error(err)
			}
			results[i] = a
		})
	}
	wg.Wait()
	return results
}

func TestAuthorizeRefreshFailure(t *testing.T) {
	errServer := errors.New("server down")
	tests := []struct {
		name    string
		refresh string // saved refresh token
		wantErr error
	}{
		{"refresh fails", "r1", errServer},
		{"no refresh token", "", ErrNotAuthenticated},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := &fakeFlow{refresh: func(context.Context, OAuthToken) (OAuthToken, error) {
				return OAuthToken{}, errServer
			}}
			s := newAccounts(t, "", flow)
			store(t, s, AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{
				idWork: OAuthToken{Access: "old", Refresh: tt.refresh, Expiry: time.Now().Add(-time.Minute)},
			}})
			if _, err := s.Authorize(t.Context(), testOpenAI, nil); !errors.Is(err, tt.wantErr) {
				t.Errorf("Authorize error = %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestAuthorizeRejected(t *testing.T) {
	later := time.Now().Add(time.Hour)
	tests := []struct {
		name          string
		entry         AuthEntry
		rejected      Authorization
		want          Authorization
		wantRefreshes int32
		wantErr       error
	}{
		{
			name:          "refreshes the rejected token",
			entry:         AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: OAuthToken{Access: "old", Refresh: "r", Expiry: later}}},
			rejected:      oauthAuth("old"),
			want:          oauthAuth("new"),
			wantRefreshes: 1,
		},
		{
			name:     "already replaced",
			entry:    AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: OAuthToken{Access: "replaced", Refresh: "r", Expiry: later}}},
			rejected: oauthAuth("old"),
			want:     oauthAuth("replaced"),
		},
		{
			name: "active account switched",
			entry: AuthEntry{Active: idHome, Accounts: map[AccountID]Credential{
				idWork: OAuthToken{Access: "old", Refresh: "r", Expiry: later},
				idHome: APIKey("sk-b"),
			}},
			rejected: oauthAuth("old"),
			want:     apiKeyAuth("sk-b"),
		},
		{
			name:     "rejected API key",
			entry:    AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: APIKey("sk")}},
			rejected: apiKeyAuth("sk"),
			wantErr:  ErrNotAuthenticated,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var refreshes atomic.Int32
			flow := &fakeFlow{refresh: func(context.Context, OAuthToken) (OAuthToken, error) {
				refreshes.Add(1)
				return OAuthToken{Access: "new", Expiry: later}, nil
			}}
			s := newAccounts(t, "", flow)
			store(t, s, tt.entry)

			got, err := s.For(testOpenAI).Authorize(t.Context(), &tt.rejected)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Authorize error = %v, want %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("Authorize = %+v, want %+v", got, tt.want)
			}
			if n := refreshes.Load(); n != tt.wantRefreshes {
				t.Errorf("refreshed %d times, want %d", n, tt.wantRefreshes)
			}
		})
	}
}

// TestAuthorizeRejectedOnce checks that concurrent callers reporting the same
// rejected token refresh it once.
func TestAuthorizeRejectedOnce(t *testing.T) {
	var refreshes atomic.Int32
	flow := &fakeFlow{refresh: func(context.Context, OAuthToken) (OAuthToken, error) {
		refreshes.Add(1)
		time.Sleep(50 * time.Millisecond)
		return OAuthToken{Access: "new", Expiry: time.Now().Add(time.Hour)}, nil
	}}
	s := newAccounts(t, "", flow)
	store(t, s, AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{
		idWork: OAuthToken{Access: "old", Refresh: "r", Expiry: time.Now().Add(time.Hour)},
	}})

	tokens := concurrently(10, func() (Authorization, error) {
		rejected := oauthAuth("old")
		return s.Authorize(t.Context(), testOpenAI, &rejected)
	}, t)
	if n := refreshes.Load(); n != 1 {
		t.Errorf("refreshed %d times, want 1", n)
	}
	if i := slices.IndexFunc(tokens, func(a Authorization) bool { return a != oauthAuth("new") }); i >= 0 {
		t.Errorf("caller %d got %+v, want the new token", i, tokens[i])
	}
}

func TestStatus(t *testing.T) {
	accounts := map[AccountID]Credential{idWork: OAuthToken{Access: "a"}, idHome: APIKey("k")}
	work := AccountStatus{ID: idWork, Kind: CredentialOAuth, Source: CredentialSourceStore}
	home := AccountStatus{ID: idHome, Kind: CredentialAPIKey, Source: CredentialSourceStore}
	config := AccountStatus{ID: ConfigAccount, Kind: CredentialAPIKey, Source: CredentialSourceConfig}
	tests := []struct {
		name  string
		key   APIKey
		entry AuthEntry
		want  AuthStatus
	}{
		{name: "none", want: AuthStatus{Provider: testOpenAI}},
		{
			name: "config key only",
			key:  "sk",
			want: AuthStatus{Provider: testOpenAI, Active: ConfigAccount, Accounts: []AccountStatus{config}},
		},
		{
			name:  "saved accounts and config key",
			key:   "sk",
			entry: AuthEntry{Active: idWork, Accounts: accounts},
			want:  AuthStatus{Provider: testOpenAI, Active: idWork, Accounts: []AccountStatus{config, home, work}},
		},
		{
			name:  "none active",
			entry: AuthEntry{Accounts: accounts},
			want:  AuthStatus{Provider: testOpenAI, Active: idHome, Accounts: []AccountStatus{home, work}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAccounts(t, tt.key, noRefresh(t))
			if tt.entry.Accounts != nil {
				store(t, s, tt.entry)
			}
			got, err := s.Status(t.Context(), testOpenAI)
			if err != nil {
				t.Fatal(err)
			}
			if got.Provider != tt.want.Provider || got.Active != tt.want.Active || !slices.Equal(got.Accounts, tt.want.Accounts) {
				t.Errorf("Status = %+v, want %+v", got, tt.want)
			}
			if got.Authenticated() != (tt.want.Active != "") {
				t.Errorf("Authenticated = %v", got.Authenticated())
			}
		})
	}
}

func TestLogin(t *testing.T) {
	errDenied := errors.New("access denied")
	tests := []struct {
		name       string
		provider   ProviderID
		account    AccountID
		method     LoginMethod
		result     error // error of the flow's login step
		wantErr    error
		wantPrompt LoginPrompt
	}{
		{name: "browser", provider: testOpenAI, account: idWork, method: LoginBrowser, wantPrompt: LoginPrompt{URL: "https://login.test/authorize"}},
		{name: "device", provider: testOpenAI, account: idWork, method: LoginDevice, wantPrompt: LoginPrompt{URL: "https://login.test/device", Code: "ABCD-1234"}},
		{name: "flow fails", provider: testOpenAI, account: idWork, method: LoginBrowser, result: errDenied, wantErr: errDenied, wantPrompt: LoginPrompt{URL: "https://login.test/authorize"}},
		{name: "no flow", provider: testAnthropic, account: idWork, method: LoginBrowser, wantErr: errors.ErrUnsupported},
		{name: "config account", provider: testOpenAI, account: ConfigAccount, method: LoginBrowser, wantErr: ErrReadOnlyAccount},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			flow := &fakeFlow{login: func(context.Context) (OAuthToken, error) {
				return OAuthToken{Access: "a"}, tt.result
			}}
			s := newAccounts(t, "", flow)
			store(t, s, AuthEntry{Active: idHome, Accounts: map[AccountID]Credential{idHome: APIKey("k")}})

			var prompts []LoginPrompt
			err := s.Login(t.Context(), tt.provider, tt.account, tt.method, func(p LoginPrompt) { prompts = append(prompts, p) })
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("Login error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantPrompt != (LoginPrompt{}) && !slices.Equal(prompts, []LoginPrompt{tt.wantPrompt}) {
				t.Errorf("prompts = %+v, want %+v", prompts, tt.wantPrompt)
			}
			st, err := s.Status(t.Context(), tt.provider)
			if err != nil {
				t.Fatal(err)
			}
			wantActive := AccountID("")
			switch {
			case tt.wantErr == nil:
				wantActive = tt.account
			case tt.provider == testOpenAI:
				wantActive = idHome
			}
			if st.Active != wantActive {
				t.Errorf("active account = %q, want %q", st.Active, wantActive)
			}
		})
	}
}

func TestLoginRejectsUnknownMethod(t *testing.T) {
	s := newAccounts(t, "", &fakeFlow{})
	if err := s.Login(t.Context(), testOpenAI, idWork, "carrier-pigeon", func(LoginPrompt) {}); err == nil {
		t.Error("Login accepted an unknown method")
	}
}

// TestLoginCancelsPrevious checks that a new login of a provider ends the one
// in progress, which holds the callback port the new one needs.
func TestLoginCancelsPrevious(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	flow := &fakeFlow{login: func(ctx context.Context) (OAuthToken, error) {
		if calls.Add(1) == 1 {
			close(started)
			<-ctx.Done()
			return OAuthToken{}, ctx.Err()
		}
		return OAuthToken{Access: "a"}, nil
	}}
	s := newAccounts(t, "", flow)

	first := make(chan error, 1)
	go func() { first <- s.Login(t.Context(), testOpenAI, idWork, LoginBrowser, func(LoginPrompt) {}) }()
	<-started

	if err := s.Login(t.Context(), testOpenAI, idHome, LoginDevice, func(LoginPrompt) {}); err != nil {
		t.Fatalf("second login: %v", err)
	}
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Errorf("first login error = %v, want context.Canceled", err)
	}
}

func TestSetKey(t *testing.T) {
	tests := []struct {
		name    string
		account AccountID
		key     APIKey
		want    Credential
		wantErr error
	}{
		{name: "trims white space", account: idWork, key: "  sk-1\n", want: APIKey("sk-1")},
		{name: "empty", account: idWork, key: " \t", wantErr: ErrEmptyKey},
		{name: "config account", account: ConfigAccount, key: "sk-1", wantErr: ErrReadOnlyAccount},
		{name: "invalid account", account: "Work", key: "sk-1", wantErr: ErrInvalidAccountID},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAccounts(t, "", &fakeFlow{})
			store(t, s, AuthEntry{Active: idHome, Accounts: map[AccountID]Credential{idHome: APIKey("k")}})
			err := s.SetKey(t.Context(), testOpenAI, tt.account, tt.key)
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("SetKey error = %v, want %v", err, tt.wantErr)
			}
			e := saved(t, s)
			if tt.wantErr != nil {
				if e.Active != idHome || len(e.Accounts) != 1 {
					t.Errorf("failed SetKey changed the entry to %#v", e)
				}
				return
			}
			if e.Accounts[tt.account] != tt.want || e.Active != tt.account {
				t.Errorf("entry = %#v, want %q active with %#v", e, tt.account, tt.want)
			}
		})
	}
}

func TestUse(t *testing.T) {
	tests := []struct {
		name       string
		key        APIKey
		account    AccountID
		wantErr    error
		wantActive AccountID
	}{
		{name: "saved account", account: idHome, wantActive: idHome},
		{name: "config account", key: "sk", account: ConfigAccount, wantActive: ConfigAccount},
		{name: "config account without key", account: ConfigAccount, wantErr: ErrUnknownAccount, wantActive: idWork},
		{name: "unknown account", account: idGone, wantErr: ErrUnknownAccount, wantActive: idWork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAccounts(t, tt.key, noRefresh(t))
			store(t, s, AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: APIKey("w"), idHome: APIKey("h")}})

			if err := s.Use(t.Context(), testOpenAI, tt.account); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Use error = %v, want %v", err, tt.wantErr)
			}
			st, err := s.Status(t.Context(), testOpenAI)
			if err != nil {
				t.Fatal(err)
			}
			if st.Active != tt.wantActive {
				t.Errorf("active account = %q, want %q", st.Active, tt.wantActive)
			}
		})
	}
}

func TestLogout(t *testing.T) {
	three := map[AccountID]Credential{idWork: APIKey("w"), idHome: APIKey("h"), idZed: APIKey("z")}
	one := map[AccountID]Credential{idWork: APIKey("w")}
	tests := []struct {
		name       string
		key        APIKey
		accounts   map[AccountID]Credential
		account    AccountID
		wantErr    error
		wantActive AccountID // in every environment
		wantMarked AccountID // in auth.json
	}{
		{name: "inactive account", accounts: three, account: idHome, wantActive: idWork, wantMarked: idWork},
		{name: "active account passes to the first by name", accounts: three, account: idWork, wantActive: idHome, wantMarked: idHome},
		{name: "config key does not take over from saved accounts", key: "sk", accounts: three, account: idWork, wantActive: idHome, wantMarked: idHome},
		{name: "last account passes to config", key: "sk", accounts: one, account: idWork, wantActive: ConfigAccount},
		{name: "last account", accounts: one, account: idWork},
		{name: "config account", key: "sk", accounts: three, account: ConfigAccount, wantErr: ErrReadOnlyAccount, wantActive: idWork, wantMarked: idWork},
		{name: "unknown account", accounts: three, account: idGone, wantErr: ErrUnknownAccount, wantActive: idWork, wantMarked: idWork},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newAccounts(t, tt.key, noRefresh(t))
			store(t, s, AuthEntry{Active: idWork, Accounts: maps.Clone(tt.accounts)})

			if err := s.Logout(t.Context(), testOpenAI, tt.account); !errors.Is(err, tt.wantErr) {
				t.Fatalf("Logout error = %v, want %v", err, tt.wantErr)
			}
			st, err := s.Status(t.Context(), testOpenAI)
			if err != nil {
				t.Fatal(err)
			}
			if st.Active != tt.wantActive {
				t.Errorf("active account = %q, want %q", st.Active, tt.wantActive)
			}
			if marked := saved(t, s).Active; marked != tt.wantMarked {
				t.Errorf("auth.json marks %q active, want %q", marked, tt.wantMarked)
			}
			if tt.wantErr == nil && slices.ContainsFunc(st.Accounts, func(a AccountStatus) bool { return a.ID == tt.account }) {
				t.Errorf("account %q still listed", tt.account)
			}
		})
	}
}

func TestParseAccountID(t *testing.T) {
	tests := []struct {
		in   string
		want AccountID // empty for invalid
	}{
		{idWork, idWork},
		{"01990000-0000-7000-8000-00000000000A", "01990000-0000-7000-8000-00000000000a"},
		{"{" + idWork + "}", idWork},
		{"urn:uuid:" + idWork, idWork},
		{"01990000000070008000000000000002", idWork},
		{string(ConfigAccount), ConfigAccount},
		{"", ""},
		{"work", ""},
		{"../x", ""},
		{idWork + "0", ""},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseAccountID(tt.in)
			if wantErr := tt.want == ""; errors.Is(err, ErrInvalidAccountID) != wantErr {
				t.Fatalf("ParseAccountID(%q) error = %v, want ErrInvalidAccountID %v", tt.in, err, wantErr)
			}
			if got != tt.want {
				t.Errorf("ParseAccountID(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestNewAccountID(t *testing.T) {
	first, second := NewAccountID(), NewAccountID()
	for _, id := range []AccountID{first, second} {
		if parsed, err := ParseAccountID(string(id)); err != nil || parsed != id {
			t.Errorf("NewAccountID = %q, not a canonical UUID (err %v)", id, err)
		}
		if version := id[14]; version != '7' {
			t.Errorf("NewAccountID = %q, version %c, want 7", id, version)
		}
	}
	if first >= second {
		t.Errorf("IDs %q, %q do not sort in creation order", first, second)
	}
}

func TestConfigAccountIsNil(t *testing.T) {
	if want := AccountID(uuid.Nil().String()); ConfigAccount != want {
		t.Errorf("ConfigAccount = %q, want the Nil UUID %q", ConfigAccount, want)
	}
}

func TestParseLoginMethod(t *testing.T) {
	tests := []struct {
		in      string
		want    LoginMethod
		wantErr bool
	}{
		{"browser", LoginBrowser, false},
		{"device", LoginDevice, false},
		{"key", "", true},
		{"", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			got, err := ParseLoginMethod(tt.in)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("ParseLoginMethod(%q) = %q, %v; want %q, error %v", tt.in, got, err, tt.want, tt.wantErr)
			}
		})
	}
}
