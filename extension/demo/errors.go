package demo

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/unaquatic/flow/agent"
)

// The errors the error demo shows, one of each kind flow reports. Each is
// made as flow's own code makes it, the agent's error types or its token
// request, but from canned replies, so that no request leaves the machine.

// APIURL is the endpoint that the demo's failed request posts to.
const APIURL = "https://api.example.com/v1/responses"

// TokenURL is the token endpoint that the demo's failed login posts to.
const TokenURL = "https://auth.example.com/oauth/token"

// KeyExpiredBody is the JSON error object of KeyExpiredError, with a code
// of the provider's own beside its message.
const KeyExpiredBody = `{"error":{"type":"authentication_error","code":"api_key_expired","message":"This API key has expired. Create a new API key and replace it in your client."}}`

// BadGatewayBody is the page of BadGatewayError, as a gateway in front of a
// provider answers with.
const BadGatewayBody = "<html>\n<head><title>502 Bad Gateway</title></head>\n<body>\n<center><h1>502 Bad Gateway</h1></center>\n</body>\n</html>\n"

// TokenErrorBody is the OAuth error object of TokenError.
const TokenErrorBody = `{"error":"invalid_grant","error_description":"The authorization code has expired."}`

// KeyExpiredError returns a provider's 401 response that answers with a
// JSON error object, KeyExpiredBody.
func KeyExpiredError() *agent.ResponseError {
	return &agent.ResponseError{
		Provider:     string(Provider),
		StatusCode:   http.StatusUnauthorized,
		Message:      "This API key has expired.",
		ResponseBody: KeyExpiredBody,
	}
}

// BadGatewayError returns a provider's 502 response that answers with an
// HTML page, BadGatewayBody, rather than JSON.
func BadGatewayError() *agent.ResponseError {
	return &agent.ResponseError{
		Provider:     string(Provider),
		StatusCode:   http.StatusBadGateway,
		Message:      http.StatusText(http.StatusBadGateway),
		ResponseBody: BadGatewayBody,
	}
}

// StreamError returns an error event that a provider sends in the middle of
// a stream it opened with a 200, which has no status of its own and no
// body, only a message.
func StreamError() *agent.ResponseError {
	return &agent.ResponseError{Provider: string(Provider), Message: "Overloaded"}
}

// RequestError returns a request to APIURL whose connection closed before
// any response.
func RequestError() *agent.RequestError {
	return &agent.RequestError{Err: &url.Error{Op: "Post", URL: APIURL, Err: io.EOF}}
}

// TokenError returns the error of a login whose token endpoint answers 401
// with TokenErrorBody, as agent.RequestOAuthToken reports it: an
// *agent.TokenEndpointError.
func TokenError() error {
	client := &http.Client{Transport: CannedReply{Status: http.StatusUnauthorized, Body: TokenErrorBody}}
	req, err := http.NewRequest(http.MethodPost, TokenURL, strings.NewReader("grant_type=authorization_code"))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	_, err = agent.RequestOAuthToken(client, req)
	return err
}

// CannedReply is an http.RoundTripper that answers every request with
// Status and the JSON Body, and sends nothing.
type CannedReply struct {
	// Status is the status of the reply.
	Status int
	// Body is the JSON body of the reply.
	Body string
}

// RoundTrip closes req's body and returns the canned reply.
func (r CannedReply) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.Body != nil {
		req.Body.Close()
	}
	return &http.Response{
		Status:        fmt.Sprintf("%d %s", r.Status, http.StatusText(r.Status)),
		StatusCode:    r.Status,
		Proto:         "HTTP/1.1",
		ProtoMajor:    1,
		ProtoMinor:    1,
		Header:        http.Header{"Content-Type": {"application/json"}},
		Body:          io.NopCloser(strings.NewReader(r.Body)),
		ContentLength: int64(len(r.Body)),
		Request:       req,
	}, nil
}
