package agent

import (
	"errors"
	"strings"
	"testing"
)

// TestResponseErrorError checks that a response error shows the provider's
// raw body on one line, compacted when it is JSON and cut short when it is
// long, and its message when it has no body, after a headline that has no
// status when the error has none, as a streaming error does.
func TestResponseErrorError(t *testing.T) {
	long := strings.Repeat("é", MaxErrorBody)
	for _, tt := range []struct {
		name string
		err  *ResponseError
		want string
	}{
		{
			name: "no body",
			err:  &ResponseError{Provider: "provider-b", StatusCode: 0, Message: "Overloaded"},
			want: "provider-b response error: Overloaded",
		},
		{
			name: "json body",
			err: &ResponseError{Provider: "provider-b", StatusCode: 429, Message: "rate_limit_error",
				ResponseBody: "{\n  \"type\": \"error\",\n  \"error\": {\"type\": \"rate_limit_error\", \"message\": \"Error\"},\n  \"request_id\": \"req_1\"\n}\n"},
			want: `provider-b response error (429): {"type":"error","error":{"type":"rate_limit_error","message":"Error"},"request_id":"req_1"}`,
		},
		{
			name: "html body",
			err: &ResponseError{Provider: "provider-a", StatusCode: 502, Message: "Bad Gateway",
				ResponseBody: "<html>\n  <body>\n\t<h1>502 Bad Gateway</h1>\n  </body>\n</html>\n"},
			want: "provider-a response error (502): <html> <body> <h1>502 Bad Gateway</h1> </body> </html>",
		},
		{
			name: "long body",
			err:  &ResponseError{Provider: "provider-a", StatusCode: 500, ResponseBody: long},
			want: "provider-a response error (500): " + long[:MaxErrorBody] + "…",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTokenEndpointError checks that a token endpoint's error response
// shows as a provider's does, its status in brackets after
// ErrTokenEndpoint and then its body on one line, and that it is
// ErrTokenEndpoint.
func TestTokenEndpointError(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  *TokenEndpointError
		want string
	}{
		{name: "json body", err: &TokenEndpointError{StatusCode: 401, Body: "{\n  \"error\": \"invalid_grant\"\n}\n"},
			want: `token endpoint error (401): {"error":"invalid_grant"}`},
		{name: "no body", err: &TokenEndpointError{StatusCode: 503}, want: "token endpoint error (503)"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
			if !errors.Is(tt.err, ErrTokenEndpoint) {
				t.Errorf("errors.Is(%v, ErrTokenEndpoint) = false", tt.err)
			}
		})
	}
}
