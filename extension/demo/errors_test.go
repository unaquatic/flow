package demo

import (
	"errors"
	"testing"

	"github.com/unaquatic/flow/agent"
)

// TestErrors checks the text of each error the error demo shows, as flow's
// own error types and its token request make it: the status in brackets
// after the provider or the token endpoint, and then the body or message.
func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "json response", err: KeyExpiredError(), want: "provider-a response error (401): " + KeyExpiredBody},
		{name: "html response", err: BadGatewayError(),
			want: "provider-a response error (502): <html> <head><title>502 Bad Gateway</title></head> <body> <center><h1>502 Bad Gateway</h1></center> </body> </html>"},
		{name: "stream", err: StreamError(), want: "provider-a response error: Overloaded"},
		{name: "request", err: RequestError(), want: `request failed: Post "` + APIURL + `": EOF`},
		{name: "token", err: TokenError(), want: "token endpoint error (401): " + TokenErrorBody},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestTokenError checks that the failed login is a token endpoint's error
// response, which keeps its status and body, as the login flows report it.
func TestTokenError(t *testing.T) {
	err := TokenError()
	tok, ok := errors.AsType[*agent.TokenEndpointError](err)
	if !ok || tok.StatusCode != 401 || tok.Body != TokenErrorBody {
		t.Errorf("TokenError() = %#v, want a 401 *agent.TokenEndpointError with TokenErrorBody", err)
	}
	if !errors.Is(err, agent.ErrTokenEndpoint) {
		t.Errorf("TokenError() = %v, want it to wrap agent.ErrTokenEndpoint", err)
	}
}
