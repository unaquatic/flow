package agent

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/unaquatic/flow/product"
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
		c.deliver(callbackResult{err: errors.New("unexpected OAuth state")})
	case q.Get("error") != "":
		msg := q.Get("error") + ": " + q.Get("error_description")
		writeCallbackPage(w, http.StatusForbidden, msg)
		c.deliver(callbackResult{err: fmt.Errorf("OAuth error: %s", msg)})
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

// callbackPage is the page shown after an OAuth redirect. It looks like a
// notice in the terminal's default theme, Dark Flow: the product icon on the
// canvas, a blank row, then a "■" marker and the title in the text color,
// with the text under it in the muted color, indented two columns. A failure
// draws its marker and title in the danger color, as the terminal's error
// indicator does.
var callbackPage = template.Must(template.New("callback").Parse(`<!DOCTYPE html>
<html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} · {{.Product}}</title>
<style>
*{margin:0;padding:0;box-sizing:border-box}
html{min-height:100%;background:#1a1a1a;color:#e6e6e6;font:14px/1.5 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
body{padding:1.5em 2ch}
svg{display:block;width:16ch;height:16ch;margin-bottom:1.5em}
h1{font:inherit;white-space:pre}
p{padding-left:2ch;color:#8a8a8a}
.error h1{color:#ff7c78}
</style></head>
<body class="{{.Class}}">
<svg viewBox="0 0 16 16" shape-rendering="crispEdges" role="img" aria-label="{{.Product}}"><rect width="16" height="16" fill="{{.IconBg}}"/><path d="{{.IconPath}}" fill="{{.IconFg}}"/></svg>
<h1>■ {{.Title}}</h1>
<p>{{.Text}}</p>
</body></html>`))

// iconPath is the SVG path data of the "ƒ" of product.Icon, one unit square
// per pixel on its 16 by 16 grid.
var iconPath = IconPath(product.Icon)

// IconPath returns SVG path data that draws the pixels of icon, half-block
// art such as product.Icon, one unit square each: every rune holds the pixel
// at column x of rows 2y and 2y+1, where "▀" sets the top one, "▄" the
// bottom one, and "█" both.
func IconPath(icon string) string {
	var b strings.Builder
	pixel := func(x, y int) { fmt.Fprintf(&b, "M%d %dh1v1h-1z", x, y) }
	for y, line := range strings.Split(icon, "\n") {
		for x, r := range []rune(line) {
			if r == '▀' || r == '█' {
				pixel(x, 2*y)
			}
			if r == '▄' || r == '█' {
				pixel(x, 2*y+1)
			}
		}
	}
	return b.String()
}

// writeCallbackPage writes the redirect page with status. An empty failure
// reports success.
func writeCallbackPage(w http.ResponseWriter, status int, failure string) {
	page := struct {
		Product, Class, Title, Text string
		IconPath, IconFg, IconBg    string
	}{
		Product: product.Name, Title: "Login Successful", Text: "You can close this window and return to your terminal.",
		IconPath: iconPath, IconFg: product.IconColor, IconBg: product.IconBgColor,
	}
	if failure != "" {
		page.Class, page.Title, page.Text = "error", "Authorization Failed", failure
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if err := callbackPage.Execute(w, page); err != nil {
		slog.Warn("write OAuth redirect page", "err", err)
	}
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
