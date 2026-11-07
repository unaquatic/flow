package post

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/unaquatic/flow/agent"
)

// TestJSON checks that JSON sends its body as JSON with its header, returns
// a 200 OK response, and turns any other status into a
// *agent.ResponseError with the API's error message.
func TestJSON(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		reply   string
		wantErr *agent.ResponseError
	}{
		{name: "ok", status: http.StatusOK, reply: "data: 1\n\n"},
		{
			name:   "error message",
			status: http.StatusUnauthorized,
			reply:  `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
			wantErr: &agent.ResponseError{Provider: "test", StatusCode: http.StatusUnauthorized, Message: "invalid x-api-key",
				ResponseBody: `{"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`},
		},
		{
			name:    "plain body",
			status:  http.StatusBadGateway,
			reply:   "Bad Gateway",
			wantErr: &agent.ResponseError{Provider: "test", StatusCode: http.StatusBadGateway, Message: "Bad Gateway", ResponseBody: "Bad Gateway"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var got map[string]string
				if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" ||
					r.Header.Get("X-Api-Key") != "key" || json.NewDecoder(r.Body).Decode(&got) != nil || got["model"] != "m" {
					t.Errorf("request = %s %v, want a JSON POST with the header and body", r.Method, r.Header)
				}
				w.WriteHeader(tt.status)
				io.WriteString(w, tt.reply)
			}))
			defer srv.Close()

			resp, err := JSON(t.Context(), srv.Client(), "test", srv.URL, http.Header{"X-Api-Key": {"key"}}, map[string]string{"model": "m"})
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("JSON: %v", err)
				}
				defer resp.Body.Close()
				if body, _ := io.ReadAll(resp.Body); string(body) != tt.reply {
					t.Errorf("body = %q, want %q", body, tt.reply)
				}
				return
			}
			respErr, ok := errors.AsType[*agent.ResponseError](err)
			if !ok {
				t.Fatalf("JSON error = %v, want a *agent.ResponseError", err)
			}
			respErr.ResponseHeaders = nil
			if !reflect.DeepEqual(respErr, tt.wantErr) {
				t.Errorf("error = %+v, want %+v", *respErr, *tt.wantErr)
			}
		})
	}
}

// TestJSONUnreachable checks that a request that cannot be sent fails with a
// *agent.RequestError.
func TestJSONUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	srv.Close()
	_, err := JSON(t.Context(), http.DefaultClient, "test", srv.URL, nil, map[string]string{})
	if _, ok := errors.AsType[*agent.RequestError](err); !ok {
		t.Errorf("error = %v, want a *agent.RequestError", err)
	}
}
