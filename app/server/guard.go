package server

import (
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/unaquatic/flow/app"
)

// Guard returns next behind the checks that keep other machines' and other
// sites' requests out:
//
//   - A server on the loopback interface takes requests that name a
//     loopback host alone, so that a site that points its own domain name at
//     the loopback address cannot reach it.
//   - A request from a browser page, which names its origin, is taken from
//     a page served from the loopback interface or one that Options.CORS
//     lists, and its response lets the page read it. A preflight request of
//     such a page is answered here.
//   - When Options.Password is set, a request must present the credentials.
//
// A request that fails a check is answered with an error response.
func (s *Server) Guard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Loopback() && !isLoopback(hostOnly(r.Host)) {
			respondError(w, app.NewError(app.CodeHostNotAllowed, fmt.Sprintf("host %q is not a loopback host", r.Host)))
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			if !s.allowedOrigin(origin) {
				respondError(w, app.NewError(app.CodeOriginNotAllowed, fmt.Sprintf("origin %q may not call the API; serve with --cors %s to allow it", origin, origin)))
				return
			}
			h := w.Header()
			h.Set("Access-Control-Allow-Origin", origin)
			h.Add("Vary", "Origin")
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE")
				h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
				h.Set("Access-Control-Max-Age", "86400")
				w.WriteHeader(http.StatusNoContent)
				return
			}
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", `Basic realm="flow"`)
			respondError(w, app.NewError(app.CodeUnauthorized, "missing or wrong server credentials"))
			return
		}
		next.ServeHTTP(w, r)
	})
}

// allowedOrigin reports whether a page of origin may call the API: one
// served over HTTP from the loopback interface, or one Options.CORS lists.
func (s *Server) allowedOrigin(origin string) bool {
	if slices.Contains(s.opts.CORS, origin) {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	return isLoopback(u.Hostname())
}

// authorized reports whether r presents the server's credentials, or the
// server asks for none.
func (s *Server) authorized(r *http.Request) bool {
	if s.opts.Password == "" {
		return true
	}
	user, pass, ok := r.BasicAuth()
	if token := r.URL.Query().Get(AuthTokenQuery); token != "" {
		user, pass, ok = decodeToken(token)
	}
	if !ok {
		return false
	}
	userOK := subtle.ConstantTimeCompare([]byte(user), []byte(s.opts.user()))
	passOK := subtle.ConstantTimeCompare([]byte(pass), []byte(s.opts.Password))
	return userOK&passOK == 1
}

// decodeToken returns the user name and password an AuthTokenQuery value
// holds, and reports whether it holds them.
func decodeToken(token string) (user, pass string, ok bool) {
	b, err := base64.StdEncoding.DecodeString(token)
	if err != nil {
		return "", "", false
	}
	return strings.Cut(string(b), ":")
}

// hostOnly returns the host of hostport, a Host header, without its port.
func hostOnly(hostport string) string {
	if host, _, err := net.SplitHostPort(hostport); err == nil {
		return host
	}
	return strings.Trim(hostport, "[]")
}

// isLoopback reports whether host, a host name or IP address, names the
// loopback interface.
func isLoopback(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
