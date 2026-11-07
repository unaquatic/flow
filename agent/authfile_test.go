package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// Account IDs in the order the accounts were added.
const (
	idHome = "01990000-0000-7000-8000-000000000001"
	idWork = "01990000-0000-7000-8000-000000000002"
	idZed  = "01990000-0000-7000-8000-000000000003"
	idGone = "01990000-0000-7000-8000-0000000000ff" // no account has it
)

// equalCredential reports whether a and b hold the same credential.
func equalCredential(a, b Credential) bool {
	ta, okA := a.(OAuthToken)
	tb, okB := b.(OAuthToken)
	if okA && okB {
		return ta.Access == tb.Access && ta.Refresh == tb.Refresh && ta.Expiry.Equal(tb.Expiry)
	}
	return a == b
}

// equalEntry reports whether a and b hold the same accounts and active
// account.
func equalEntry(a, b AuthEntry) bool {
	return a.Active == b.Active && maps.EqualFunc(a.Accounts, b.Accounts, equalCredential)
}

// writeAuthFile writes contents as the auth.json of dir.
func writeAuthFile(t *testing.T, dir, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

// readAuthFile returns the entries of the auth.json of dir.
func readAuthFile(t *testing.T, dir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, fileName))
	if err != nil {
		t.Fatal(err)
	}
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	return entries
}

// set is an Update change that saves c as account id without activating it.
func set(id AccountID, c Credential) func(*AuthEntry) error {
	return func(e *AuthEntry) error {
		e.Accounts[id] = c
		return nil
	}
}

func TestFileLoad(t *testing.T) {
	expiry := time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC)
	tests := []struct {
		name    string
		file    string // auth.json contents; empty for no file
		want    AuthEntry
		wantErr bool
	}{
		{name: "no file"},
		{name: "no entry", file: `{"anthropic":{"accounts":[{"id":"` + idHome + `","type":"api_key","key":"k"}]}}`},
		{
			name: "accounts",
			file: `{"openai":{"active":"` + idWork + `","accounts":[
				{"id":"` + idHome + `","type":"api_key","key":"sk-1"},
				{"id":"` + idWork + `","type":"oauth","access_token":"a","refresh_token":"r","expires_at":1893553445}]}}`,
			want: AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{
				idWork: OAuthToken{Access: "a", Refresh: "r", Expiry: expiry},
				idHome: APIKey("sk-1"),
			}},
		},
		{
			name: "IDs in other forms",
			file: `{"openai":{"active":"{01990000-0000-7000-8000-000000000002}","accounts":[
				{"id":"01990000000070008000000000000002","type":"api_key","key":"k"}]}}`,
			want: AuthEntry{Active: idWork, Accounts: map[AccountID]Credential{idWork: APIKey("k")}},
		},
		{name: "entry in the old format has no accounts", file: `{"openai":{"type":"api_key","key":"sk-1"}}`},
		{name: "accounts in a map", file: `{"openai":{"accounts":{"` + idHome + `":{"type":"api_key","key":"k"}}}}`, wantErr: true},
		{name: "corrupt file", file: `{`, wantErr: true},
		{name: "account without id", file: `{"openai":{"accounts":[{"type":"api_key","key":"k"}]}}`, wantErr: true},
		{
			name:    "id twice",
			file:    `{"openai":{"accounts":[{"id":"` + idHome + `","type":"api_key","key":"k"},{"id":"` + idHome + `","type":"api_key","key":"k2"}]}}`,
			wantErr: true,
		},
		{name: "unknown credential type", file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"cookie"}]}}`, wantErr: true},
		{name: "api key without key", file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"api_key"}]}}`, wantErr: true},
		{name: "oauth without access token", file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"oauth"}]}}`, wantErr: true},
		{
			name:    "expiry not a Unix time",
			file:    `{"openai":{"accounts":[{"id":"` + idHome + `","type":"oauth","access_token":"a","expires_at":"2030-01-02T03:04:05Z"}]}}`,
			wantErr: true,
		},
		{name: "account ID not a UUID", file: `{"openai":{"accounts":[{"id":"work","type":"api_key","key":"k"}]}}`, wantErr: true},
		{name: "active ID not a UUID", file: `{"openai":{"active":"work"}}`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			if tt.file != "" {
				writeAuthFile(t, dir, tt.file)
			}
			got, err := AuthFile{Dir: dir}.Load(testOpenAI)
			if (err != nil) != tt.wantErr {
				t.Fatalf("Load error = %v, want error %v", err, tt.wantErr)
			}
			if !equalEntry(got, tt.want) {
				t.Errorf("Load = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestFileUpdate(t *testing.T) {
	tests := []struct {
		name string
		cred Credential
	}{
		{"api key", APIKey("sk-1")},
		{"oauth token", OAuthToken{Access: "a", Refresh: "r", Expiry: time.Now().Add(time.Hour).Truncate(time.Second)}},
		{"oauth token without expiry", OAuthToken{Access: "a"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAuthFile(t, dir, `{"anthropic":{"accounts":[{"id":"`+idHome+`","type":"api_key","key":"other"}]},"future":{"accounts":[{"id":"x","type":"cookie"}]}}`)
			f := AuthFile{Dir: dir}

			if err := f.Update(t.Context(), testOpenAI, set(idWork, tt.cred)); err != nil {
				t.Fatal(err)
			}
			got, err := f.Load(testOpenAI)
			if err != nil {
				t.Fatal(err)
			}
			if want := (AuthEntry{Accounts: map[AccountID]Credential{idWork: tt.cred}}); !equalEntry(got, want) {
				t.Errorf("Load = %#v, want %#v", got, want)
			}
			if other, err := f.Load(testAnthropic); err != nil || other.Accounts[idHome] != APIKey("other") {
				t.Errorf("other provider = %#v, %v; want its entry kept", other, err)
			}
			if _, ok := readAuthFile(t, dir)["future"]; !ok {
				t.Error("entry this version cannot decode was dropped")
			}
		})
	}
}

// TestFileUpdateWritesAccountArray checks that an entry is saved with its
// accounts in an array sorted by ID, each object holding its id.
func TestFileUpdateWritesAccountArray(t *testing.T) {
	dir := t.TempDir()
	err := AuthFile{Dir: dir}.Update(t.Context(), testOpenAI, func(e *AuthEntry) error {
		e.Accounts[idZed] = APIKey("z")
		e.Accounts[idHome] = APIKey("h")
		e.Accounts[idWork] = OAuthToken{Access: "w"}
		e.Active = idWork
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var saved struct {
		Active   AccountID `json:"active"`
		Accounts []struct {
			ID   AccountID `json:"id"`
			Type string    `json:"type"`
		} `json:"accounts"`
	}
	if err := json.Unmarshal(readAuthFile(t, dir)["openai"], &saved); err != nil {
		t.Fatalf("openai entry is not an account array: %v", err)
	}
	var ids []AccountID
	for _, a := range saved.Accounts {
		ids = append(ids, a.ID)
	}
	if want := []AccountID{idHome, idWork, idZed}; !slices.Equal(ids, want) {
		t.Errorf("saved account IDs %q, want %q", ids, want)
	}
	if saved.Active != idWork || saved.Accounts[1].Type != "oauth" {
		t.Errorf("saved %+v, want active %s holding the oauth token", saved, idWork)
	}
}

// TestFileUpdateWritesUnixExpiry checks that a token expiry is saved as a
// Unix time in seconds, and left out when the token does not expire.
func TestFileUpdateWritesUnixExpiry(t *testing.T) {
	dir := t.TempDir()
	err := AuthFile{Dir: dir}.Update(t.Context(), testOpenAI, func(e *AuthEntry) error {
		e.Accounts[idHome] = OAuthToken{Access: "h", Expiry: time.Date(2030, 1, 2, 3, 4, 5, 999_000_000, time.UTC)}
		e.Accounts[idWork] = OAuthToken{Access: "w"}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	var saved struct {
		Accounts []map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(readAuthFile(t, dir)["openai"], &saved); err != nil {
		t.Fatal(err)
	}
	if got := string(saved.Accounts[0]["expires_at"]); got != "1893553445" {
		t.Errorf("saved expires_at %s, want 1893553445", got)
	}
	if got, ok := saved.Accounts[1]["expires_at"]; ok {
		t.Errorf("saved expires_at %s for a token without expiry, want none", got)
	}
}

func TestFileUpdateFailures(t *testing.T) {
	errChange := errors.New("change failed")
	tests := []struct {
		name   string
		file   string
		change func(*AuthEntry) error
	}{
		{name: "corrupt file", file: "{", change: set(idHome, APIKey("k"))},
		{name: "corrupt entry", file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"cookie"}]}}`, change: set(idWork, APIKey("k"))},
		{
			name:   "change fails",
			file:   `{"openai":{"accounts":[{"id":"` + idHome + `","type":"api_key","key":"k"}]}}`,
			change: func(e *AuthEntry) error { delete(e.Accounts, idHome); return errChange },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAuthFile(t, dir, tt.file)
			if err := (AuthFile{Dir: dir}).Update(t.Context(), testOpenAI, tt.change); err == nil {
				t.Fatal("Update succeeded")
			}
			if data, err := os.ReadFile(filepath.Join(dir, fileName)); err != nil || string(data) != tt.file {
				t.Errorf("auth.json = %q, %v; want it unchanged", data, err)
			}
		})
	}
}

func TestFileUpdateRemovesEmpty(t *testing.T) {
	tests := []struct {
		name string
		file string
	}{
		{
			name: "keeps other providers",
			file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"api_key","key":"k"}]},"anthropic":{"active":"` + string(ConfigAccount) + `"}}`,
		},
		{name: "keeps file with last entry", file: `{"openai":{"accounts":[{"id":"` + idHome + `","type":"api_key","key":"k"}]}}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			writeAuthFile(t, dir, tt.file)
			err := AuthFile{Dir: dir}.Update(t.Context(), testOpenAI, func(e *AuthEntry) error {
				delete(e.Accounts, idHome)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := readAuthFile(t, dir)["openai"]; ok {
				t.Error("empty openai entry kept")
			}
		})
	}
}

func TestFileMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no Unix permission bits")
	}
	dir := filepath.Join(t.TempDir(), "home")
	if err := (AuthFile{Dir: dir}).Update(t.Context(), testOpenAI, set(idHome, APIKey("k"))); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]fs.FileMode{".": 0o700, fileName: 0o600} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %v, want %v", name, got, want)
		}
	}
}

// TestFileConcurrentUpdate updates from many goroutines, each taking the
// lock of auth.json on its own, and checks that no update is lost.
func TestFileConcurrentUpdate(t *testing.T) {
	dir := t.TempDir()
	const n = 20
	ids := make([]AccountID, n)
	for i := range ids {
		ids[i] = NewAccountID()
	}
	var wg sync.WaitGroup
	for i, id := range ids {
		wg.Go(func() {
			// Half the goroutines share one provider to race inside an entry.
			p := ProviderID(fmt.Sprint("p", i%2))
			if err := (AuthFile{Dir: dir}).Update(t.Context(), p, set(id, APIKey("k"))); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()

	for i, id := range ids {
		e, err := AuthFile{Dir: dir}.Load(ProviderID(fmt.Sprint("p", i%2)))
		if _, ok := e.Accounts[id]; err != nil || !ok {
			t.Errorf("account %s lost (err %v)", id, err)
		}
	}
}
