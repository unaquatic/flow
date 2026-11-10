package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// PKCE is a proof key for an OAuth code exchange (RFC 7636).
type PKCE struct {
	// Verifier is sent with the code exchange.
	Verifier string
	// Challenge is the S256 challenge of Verifier, sent with the
	// authorization request.
	Challenge string
}

// NewPKCE returns a random proof key.
func NewPKCE() PKCE {
	verifier := rand.Text() + rand.Text()
	return PKCE{Verifier: verifier, Challenge: challenge(verifier)}
}

// challenge returns the S256 code challenge of verifier.
func challenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// NewOAuthState returns a random OAuth state value.
func NewOAuthState() string {
	return rand.Text()
}

// OAuthCallback receives one OAuth redirect on a local port.
type OAuthCallback struct {
	// RedirectURI is the URI to send as redirect_uri.
	RedirectURI string

	srv    *http.Server
	result chan callbackResult
}

// callbackResult is the outcome of an OAuth redirect.
type callbackResult struct {
	code string
	err  error
}

// ListenOAuthCallback starts a server on 127.0.0.1:port that accepts one
// redirect to path carrying state. Port 0 picks a free port. The caller must
// call Wait.
func ListenOAuthCallback(port int, path, state string) (*OAuthCallback, error) {
	ln, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		return nil, fmt.Errorf("listen for OAuth redirect on port %d (is another program using it?): %w", port, err)
	}
	c := &OAuthCallback{
		RedirectURI: fmt.Sprintf("http://localhost:%d%s", ln.Addr().(*net.TCPAddr).Port, path),
		result:      make(chan callbackResult, 1),
	}
	mux := http.NewServeMux()
	mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) { c.handle(w, r, state) })
	c.srv = &http.Server{Handler: mux}
	go func() {
		if err := c.srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
			c.deliver(callbackResult{err: err})
		}
	}()
	return c, nil
}

// Wait returns the authorization code of the redirect, or the error it
// carries, once it arrives or ctx is done. It shuts the server down.
func (c *OAuthCallback) Wait(ctx context.Context) (string, error) {
	var res callbackResult
	select {
	case res = <-c.result:
	case <-ctx.Done():
		res.err = ctx.Err()
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := c.srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("shut down OAuth redirect server", "err", err)
	}
	return res.code, res.err
}

// handle answers the redirect and delivers its outcome.
func (c *OAuthCallback) handle(w http.ResponseWriter, r *http.Request, state string) {
	q := r.URL.Query()
	switch {
	case q.Get("state") != state:
		writeCallbackPage(w, http.StatusBadRequest, "Session expired or invalid. Please try logging in again.")
		c.deliver(callbackResult{err: errors.New("OAuth redirect with unexpected state")})
	case q.Get("error") != "":
		msg := q.Get("error") + ": " + q.Get("error_description")
		writeCallbackPage(w, http.StatusForbidden, msg)
		c.deliver(callbackResult{err: errors.New("OAuth error: " + msg)})
	case q.Get("code") == "":
		writeCallbackPage(w, http.StatusBadRequest, "Missing authorization code.")
		c.deliver(callbackResult{err: ErrNoAuthCode})
	default:
		writeCallbackPage(w, http.StatusOK, "")
		c.deliver(callbackResult{code: q.Get("code")})
	}
}

// deliver records the first outcome and drops later ones.
func (c *OAuthCallback) deliver(r callbackResult) {
	select {
	case c.result <- r:
	default:
	}
}

// callbackPage is the page shown after an OAuth redirect.
const callbackPage = `<!DOCTYPE html>
<html><head><meta charset="utf-8"><title>Flow</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
html,body{height:100%%;background:#0a0a0a;color:#e0e0e0;font-family:monospace}
body{padding:1rem}
.card{text-align:left;padding:1rem;border:1px solid #333;background:#111}
h2{font-size:1rem;font-weight:600;margin-bottom:.25rem}
p{font-size:1rem;color:#888}
.error h2,.error p{color:#e05050}
</style></head>
<body><div class="card %s"><h2>%s</h2><p>%s</p></div></body></html>`

// writeCallbackPage writes the redirect page with status. An empty failure
// reports success.
func writeCallbackPage(w http.ResponseWriter, status int, failure string) {
	class, title, text := "", "Login Successful", "You can close this window and return to your terminal."
	if failure != "" {
		class, title, text = "error", "Authorization Failed", failure
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	fmt.Fprintf(w, callbackPage, class, title, html.EscapeString(text))
}

// OpenBrowser opens url in the default browser.
func OpenBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		if err := cmd.Wait(); err != nil {
			slog.Info("open browser", "err", err)
		}
	}()
	return nil
}

// RequestOAuthToken sends req to an OAuth token endpoint and returns the token
// in its response.
func RequestOAuthToken(client *http.Client, req *http.Request) (OAuthToken, error) {
	resp, err := client.Do(req)
	if err != nil {
		return OAuthToken{}, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return OAuthToken{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return OAuthToken{}, &TokenEndpointError{StatusCode: resp.StatusCode, Body: string(body)}
	}
	return ParseOAuthToken(body)
}

// ParseOAuthToken decodes an OAuth token response body, which must carry an
// access token.
func ParseOAuthToken(data []byte) (OAuthToken, error) {
	var r struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return OAuthToken{}, fmt.Errorf("%w: %w", ErrTokenEndpoint, err)
	}
	if r.AccessToken == "" {
		return OAuthToken{}, fmt.Errorf("%w: response without access_token", ErrTokenEndpoint)
	}
	tok := OAuthToken{Access: r.AccessToken, Refresh: r.RefreshToken}
	if r.ExpiresIn > 0 {
		tok.Expiry = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second)
	}
	return tok, nil
}
