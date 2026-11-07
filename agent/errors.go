package agent

import (
	"errors"
	"fmt"
	"net/http"
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
	// variable that is not set, such as Azure's resource name.
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

// Error returns the provider, status, and message.
func (e *ResponseError) Error() string {
	return fmt.Sprintf("%s response error (%d): %s", e.Provider, e.StatusCode, e.Message)
}
