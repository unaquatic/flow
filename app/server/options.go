package server

import "time"

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
	// Heartbeat is how often GET /event sends a keepalive comment while no
	// event comes. Zero is DefaultHeartbeat.
	Heartbeat time.Duration
}

// DefaultHeartbeat is how often GET /event sends a keepalive comment when
// Options names no Heartbeat: often enough for proxies that close a
// connection idle for 30 seconds or more.
const DefaultHeartbeat = 15 * time.Second

// heartbeat returns Heartbeat, or DefaultHeartbeat.
func (o Options) heartbeat() time.Duration {
	if o.Heartbeat <= 0 {
		return DefaultHeartbeat
	}
	return o.Heartbeat
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
