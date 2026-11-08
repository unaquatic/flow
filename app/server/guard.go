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

// DefaultHost is the address the server listens on when Options names none:
// the loopback interface, which only programs on the same machine reach.
const DefaultHost = "127.0.0.1"

// DefaultUsername is the user name of the server's credentials when Options
// names none.
const DefaultUsername = "flow"

// AuthTokenQuery is the query parameter that carries the server's
// credentials, the base64 of "username:password", for a client that cannot
// set the Authorization header, such as a browser's EventSource.
const AuthTokenQuery = "auth_token"

// Options configure where the server listens and which requests it takes.
type Options struct {
	// Hostname is the host name or IP address to listen on. Empty listens on
	// DefaultHost.
	Hostname string
	// Port is the TCP port to listen on. Zero picks a free port.
	Port int
	// CORS lists the origins of browser pages, such as
	// "https://app.example", that may call the API besides the pages served
	// from the loopback interface, which always may.
	CORS []string
	// Username is the user name of the credentials Password asks for. Empty
	// is DefaultUsername.
	Username string
	// Password, when set, makes every request present it, with Username,
	// by HTTP basic authentication or in the AuthTokenQuery parameter.
	Password string
}

// Host returns the host the server listens on: Hostname, or DefaultHost.
func (o Options) Host() string {
	if o.Hostname == "" {
		return DefaultHost
	}
	return o.Hostname
}

// Loopback reports whether the server listens on the loopback interface
// alone, which no other machine reaches.
func (o Options) Loopback() bool {
	return isLoopback(o.Host())
}

// user returns the user name of the server's credentials.
func (o Options) user() string {
	if o.Username == "" {
		return DefaultUsername
	}
	return o.Username
}

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
