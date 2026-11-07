package google

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
)

// tokenServer is a Google token endpoint that answers every request with a
// new token, numbered, after check approves the request's form.
func tokenServer(t *testing.T, check func(t *testing.T, form map[string]string)) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		form := map[string]string{}
		for k := range r.PostForm {
			form[k] = r.PostForm.Get(k)
		}
		check(t, form)
		json.NewEncoder(w).Encode(map[string]any{"access_token": "tok-" + string(rune('0'+n.Add(1))), "expires_in": 3600})
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

// writeFile writes f as the credentials file GOOGLE_APPLICATION_CREDENTIALS
// names, and returns the environment.
func writeFile(t *testing.T, f File) func(string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "adc.json")
	data, _ := json.Marshal(f)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return func(k string) string {
		if k == "GOOGLE_APPLICATION_CREDENTIALS" {
			return path
		}
		return ""
	}
}

// TestServiceAccount checks that a service account's token request carries
// a JWT its key signed, for the token endpoint and the cloud-platform
// scope, and that the token is kept until a request rejects it.
func TestServiceAccount(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	der, _ := x509.MarshalPKCS8PrivateKey(key)
	srv, minted := tokenServer(t, func(t *testing.T, form map[string]string) {
		if form["grant_type"] != "urn:ietf:params:oauth:grant-type:jwt-bearer" {
			t.Errorf("grant_type = %q", form["grant_type"])
		}
		parts := strings.Split(form["assertion"], ".")
		if len(parts) != 3 {
			t.Fatalf("assertion %q is not a JWT", form["assertion"])
		}
		sig, _ := base64.RawURLEncoding.DecodeString(parts[2])
		sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, sum[:], sig); err != nil {
			t.Errorf("JWT signature: %v", err)
		}
		raw, _ := base64.RawURLEncoding.DecodeString(parts[1])
		var claims map[string]any
		json.Unmarshal(raw, &claims)
		if claims["iss"] != "sa@p.iam.gserviceaccount.com" || claims["scope"] != Scope || !strings.HasPrefix(claims["aud"].(string), "http://127.0.0.1") {
			t.Errorf("claims = %v", claims)
		}
	})
	env := writeFile(t, File{
		Type: "service_account", ClientEmail: "sa@p.iam.gserviceaccount.com", PrivateKeyID: "k1",
		PrivateKey: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})), TokenURI: srv.URL,
	})
	c := &Credentials{Env: env}

	a, err := c.Authorize(t.Context(), nil)
	if err != nil || a.Kind != agent.CredentialOAuth || a.Token != "tok-1" {
		t.Fatalf("Authorize = %+v, %v; want OAuth tok-1", a, err)
	}
	if again, _ := c.Authorize(t.Context(), nil); again.Token != "tok-1" || minted.Load() != 1 {
		t.Errorf("second Authorize = %q after %d token requests, want tok-1 kept", again.Token, minted.Load())
	}
	if replaced, _ := c.Authorize(t.Context(), &a); replaced.Token != "tok-2" {
		t.Errorf("after a rejection: %q, want a new token", replaced.Token)
	}
}

// TestAuthorizedUser checks a gcloud login's refresh, that a token about to
// expire is replaced, and the quota project header.
func TestAuthorizedUser(t *testing.T) {
	srv, minted := tokenServer(t, func(t *testing.T, form map[string]string) {
		if form["grant_type"] != "refresh_token" || form["refresh_token"] != "rt" || form["client_id"] != "cid" {
			t.Errorf("form = %v", form)
		}
	})
	env := writeFile(t, File{Type: "authorized_user", ClientID: "cid", ClientSecret: "cs", RefreshToken: "rt", TokenURI: srv.URL, QuotaProject: "quota-p"})
	now := time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	c := &Credentials{Env: env, Now: func() time.Time { return now }}
	if a, err := c.Authorize(t.Context(), nil); err != nil || a.Token != "tok-1" {
		t.Fatalf("Authorize = %+v, %v", a, err)
	}
	now = now.Add(59*time.Minute + 30*time.Second)
	if a, _ := c.Authorize(t.Context(), nil); a.Token != "tok-2" || minted.Load() != 2 {
		t.Errorf("near expiry: %q, want a new token", a.Token)
	}

	var header string
	api := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { header = r.Header.Get("X-Goog-User-Project") }))
	defer api.Close()
	resp, err := (&http.Client{Transport: c.Transport(nil)}).Get(api.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if header != "quota-p" {
		t.Errorf("x-goog-user-project = %q, want quota-p", header)
	}
}

func TestNoCredentials(t *testing.T) {
	home := t.TempDir()
	c := &Credentials{Env: func(k string) string {
		if k == "HOME" {
			return home
		}
		return ""
	}}
	if c.Available() {
		t.Error("Available with no credentials file")
	}
	if _, err := c.Authorize(t.Context(), nil); !errors.Is(err, agent.ErrNotAuthenticated) {
		t.Errorf("err = %v, want ErrNotAuthenticated", err)
	}
	// gcloud's file is found in the home directory.
	dir := filepath.Join(home, ".config", "gcloud")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "application_default_credentials.json"), []byte(`{"type":"external_account"}`), 0o600)
	if !c.Available() {
		t.Error("gcloud's credentials file not found")
	}
	if _, err := c.Authorize(t.Context(), nil); !errors.Is(err, agent.ErrNotAuthenticated) || !strings.Contains(err.Error(), "external_account") {
		t.Errorf("unsupported type: err = %v", err)
	}
}
