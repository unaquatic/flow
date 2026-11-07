package protocol

import (
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension/protocol/content"
)

// sessionEndpoints are an endpoint of each format, at base URL api.
func sessionEndpoints(api string) map[gateway.Format]gateway.Endpoint {
	bearer := gateway.Auth{Scheme: gateway.AuthBearer}
	return map[gateway.Format]gateway.Endpoint{
		FormatMessages:  {Format: FormatMessages, API: api, Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: "x-api-key"}},
		FormatResponses: {Format: FormatResponses, API: api, Auth: bearer},
		FormatChat:      {Format: FormatChat, API: api, Auth: bearer},
		FormatContent:   {Format: FormatContent, API: api, Auth: gateway.Auth{Scheme: gateway.AuthHeader, Header: content.DefaultKeyHeader}},
	}
}

// TestSessionHeader checks that every request of a SessionProvider, in
// every format, names its session in SessionHeader, its cache key or else a
// new ID of its own, and flow in UserAgent unless the request names its
// own; that the caller's headers are left as they were; and that another
// provider's requests name neither.
func TestSessionHeader(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: [DONE]\n\n")
	}))
	t.Cleanup(srv.Close)
	dial := withAuth(Dialer(Options{}), key{})
	send := func(t *testing.T, p agent.Provider, req agent.Request) http.Header {
		t.Helper()
		req.Messages = []agent.Message{agent.NewUserMessage("hi")}
		req.MaxTokens = 10
		stream, err := p.Model("m").Stream(t.Context(), req)
		if err != nil {
			t.Fatal(err)
		}
		for range stream {
		}
		return got
	}

	for format, ep := range sessionEndpoints(srv.URL + "/v1") {
		t.Run(string(format), func(t *testing.T) {
			for _, id := range SessionProviders {
				p, ok := dial(id, ep)
				if !ok {
					t.Fatalf("%s: no provider", id)
				}
				h := send(t, p, agent.Request{CacheKey: "session-1"})
				if v := h.Get(SessionHeader); v != "session-1" {
					t.Errorf("%s: %s = %q, want the cache key, session-1", id, SessionHeader, v)
				}
				if v := h.Get("User-Agent"); v != UserAgent {
					t.Errorf("%s: User-Agent = %q, want %q", id, v, UserAgent)
				}

				first := send(t, p, agent.Request{}).Get(SessionHeader)
				second := send(t, p, agent.Request{}).Get(SessionHeader)
				if first == "" || first == second {
					t.Errorf("%s: %s of requests of no session = %q, %q, want a new ID each", id, SessionHeader, first, second)
				}

				own := map[string]string{"user-agent": "client-x/1.0", "x-extra": "1"}
				before := maps.Clone(own)
				h = send(t, p, agent.Request{CacheKey: "session-1", Headers: own})
				if v := h.Get("User-Agent"); v != "client-x/1.0" {
					t.Errorf("%s: User-Agent = %q, want the request's own, client-x/1.0", id, v)
				}
				if !maps.Equal(own, before) {
					t.Errorf("%s: request headers changed to %v, want %v", id, own, before)
				}
			}

			p, ok := dial("gw", ep)
			if !ok {
				t.Fatal("gw: no provider")
			}
			h := send(t, p, agent.Request{CacheKey: "session-1"})
			if v := h.Get(SessionHeader); v != "" {
				t.Errorf("gw: %s = %q, want none", SessionHeader, v)
			}
			if v := h.Get("User-Agent"); v == UserAgent {
				t.Errorf("gw: User-Agent = %q, want the HTTP client's own", v)
			}
		})
	}
}
