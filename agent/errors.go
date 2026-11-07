package agent

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	// ErrNotAuthenticated indicates that credentials are unavailable.
	ErrNotAuthenticated = errors.New("not authenticated")

	// ErrProviderNotRegistered indicates that a provider is not registered.
	ErrProviderNotRegistered = errors.New("provider not registered")

	// ErrVariantNotFound indicates that the catalog lists no such variant of a
	// model at its provider.
	ErrVariantNotFound = errors.New("model variant not found")

	// ErrModelNotFound indicates that the catalog lists no such model at a
	// provider.
	ErrModelNotFound = errors.New("model not found in catalog")

	// ErrFormatNotSupported indicates that a provider's endpoint takes a
	// request format, or an authentication, that flow does not speak.
	ErrFormatNotSupported = errors.New("request format not supported")

	// ErrMissingVariable indicates that a base URL names an environment
	// variable that is not set, such as a cloud resource's name.
	ErrMissingVariable = errors.New("environment variable not set")
)

// RequestError reports a failure while building or sending a request.
type RequestError struct {
	// Err is the underlying request failure.
	Err error
}

// Error returns the request failure text.
func (e *RequestError) Error() string { return "request failed: " + e.Err.Error() }

// ResponseParseError reports a failure while reading or decoding a response.
type ResponseParseError struct {
	// Err is the underlying response failure.
	Err error
}

// Error returns the response parsing failure text.
func (e *ResponseParseError) Error() string { return "failed to parse response: " + e.Err.Error() }

// ResponseError describes an unsuccessful provider response.
type ResponseError struct {
	// Provider identifies the provider that returned the error.
	Provider string
	// StatusCode is the HTTP status, or zero for a streaming error.
	StatusCode int
	// Message is the provider error detail.
	Message string

	// ResponseHeaders contains response headers when available.
	ResponseHeaders http.Header

	// ResponseBody contains the raw response body when available.
	ResponseBody string
}

// MaxErrorBody is how many bytes of a response body ResponseError.Error
// shows before it cuts the body short with an ellipsis.
const MaxErrorBody = 1000

// Error returns the provider, the status, and the raw response body as the
// provider sent it, on one line, or the message when there is no body. A
// JSON body is compacted, any other has its runs of white space collapsed,
// and either is cut to MaxErrorBody bytes.
func (e *ResponseError) Error() string {
	detail := e.Message
	if body := OneLineBody(e.ResponseBody); body != "" {
		detail = body
	}
	return fmt.Sprintf("%s response error (%d): %s", e.Provider, e.StatusCode, detail)
}

// OneLineBody returns body on one line: compacted when it is JSON, with its
// runs of white space collapsed to single spaces otherwise, and cut to
// MaxErrorBody bytes, at a rune boundary, followed by an ellipsis.
func OneLineBody(body string) string {
	var buf bytes.Buffer
	if json.Compact(&buf, []byte(body)) == nil {
		body = buf.String()
	} else {
		body = strings.Join(strings.Fields(body), " ")
	}
	if len(body) <= MaxErrorBody {
		return body
	}
	cut := MaxErrorBody
	for cut > 0 && !utf8.RuneStart(body[cut]) {
		cut--
	}
	return body[:cut] + "…"
}

// RetryAfter returns how long the provider asked to wait before the next
// request, from the retry-after-ms header that some providers send or the
// standard Retry-After header in seconds or as an HTTP date, measured
// from now. It returns zero when no header asks for a wait.
func (e *ResponseError) RetryAfter(now time.Time) time.Duration {
	if ms, err := strconv.ParseFloat(e.ResponseHeaders.Get("Retry-After-Ms"), 64); err == nil && ms > 0 {
		return time.Duration(ms * float64(time.Millisecond))
	}
	v := e.ResponseHeaders.Get("Retry-After")
	if v == "" {
		return 0
	}
	if s, err := strconv.ParseFloat(v, 64); err == nil {
		return max(time.Duration(s*float64(time.Second)), 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}
