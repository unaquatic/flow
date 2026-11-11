package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"
)

func TestClassify(t *testing.T) {
	tests := []struct {
		name      string
		err       error
		retryable bool
		message   string
	}{
		{
			name: "nil error",
			err:  nil,
		},
		{
			name: "unknown error",
			err:  errors.New("something weird"),
		},
		{
			name: "context canceled",
			err:  context.Canceled,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "wrapped context canceled",
			err:  fmt.Errorf("stream: %w", context.Canceled),
		},

		{
			name:      "500 server error",
			err:       &ResponseError{Provider: "provider-b", StatusCode: 500, Message: "Internal Server Error"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "502 bad gateway",
			err:       &ResponseError{Provider: "provider-a", StatusCode: 502, Message: "Bad Gateway"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "503 service unavailable",
			err:       &ResponseError{Provider: "provider-a", StatusCode: 503, Message: "Service Unavailable"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "529 overloaded",
			err:       &ResponseError{Provider: "provider-b", StatusCode: 529, Message: "Overloaded"},
			retryable: true,
			message:   "provider overloaded",
		},
		{
			name:      "429 rate limited",
			err:       &ResponseError{Provider: "provider-b", StatusCode: 429, Message: "Rate limit reached"},
			retryable: true,
			message:   "rate limited",
		},
		{
			name: "401 unauthorized",
			err:  &ResponseError{Provider: "provider-a", StatusCode: 401, Message: "Invalid API key"},
		},
		{
			name: "403 forbidden",
			err:  &ResponseError{Provider: "provider-b", StatusCode: 403, Message: "Permission denied"},
		},
		{
			name: "400 bad request",
			err:  &ResponseError{Provider: "provider-b", StatusCode: 400, Message: "Invalid request"},
		},
		{
			name: "404 not found",
			err:  &ResponseError{Provider: "provider-a", StatusCode: 404, Message: "Model not found"},
		},

		{
			name:      "mid-stream overloaded",
			err:       &ResponseError{Provider: "provider-b", StatusCode: 0, Message: "Overloaded"},
			retryable: true,
			message:   "provider overloaded",
		},
		{
			name:      "mid-stream rate limit",
			err:       &ResponseError{Provider: "provider-a", StatusCode: 0, Message: "Rate limit reached"},
			retryable: true,
			message:   "rate limited",
		},
		{
			name:      "mid-stream too many requests",
			err:       &ResponseError{Provider: "provider-a", StatusCode: 0, Message: "Too Many Requests"},
			retryable: true,
			message:   "too many requests",
		},
		{
			name: "mid-stream unrecognized message",
			err:  &ResponseError{Provider: "provider-a", StatusCode: 0, Message: "something else entirely"},
		},

		{
			name: "body contains rate_limit_error",
			err: &ResponseError{
				Provider:     "provider-b",
				StatusCode:   0,
				Message:      "error occurred",
				ResponseBody: `{"error":{"type":"rate_limit_error","message":"Rate limit"}}`,
			},
			retryable: true,
			message:   "rate limited",
		},
		{
			name: "body contains overloaded_error",
			err: &ResponseError{
				Provider:     "provider-b",
				StatusCode:   0,
				Message:      "error occurred",
				ResponseBody: `{"error":{"type":"overloaded_error","message":"Overloaded"}}`,
			},
			retryable: true,
			message:   "provider overloaded",
		},

		{
			name:      "network error",
			err:       &RequestError{Err: errors.New("dial tcp: connection refused")},
			retryable: true,
			message:   "network error",
		},
		{
			name: "request cancelled",
			err:  &RequestError{Err: &url.Error{Op: "Post", URL: "https://api.example", Err: context.Canceled}},
		},
		{
			name: "response cut off by cancel",
			err:  &ResponseParseError{Err: fmt.Errorf("read event stream: %w", context.Canceled)},
		},
		{
			name:      "request timed out",
			err:       &RequestError{Err: &url.Error{Op: "Post", URL: "https://api.example", Err: context.DeadlineExceeded}},
			retryable: true,
			message:   "network error",
		},

		{
			name:      "wrapped 500",
			err:       fmt.Errorf("gateway: %w", &ResponseError{Provider: "provider-b", StatusCode: 500, Message: "fail"}),
			retryable: true,
			message:   "server error",
		},

		{
			name: "ErrNotAuthenticated",
			err:  ErrNotAuthenticated,
		},
		{
			name:      "429 generic error",
			err:       &ResponseError{Provider: "provider-b", StatusCode: 429, Message: "Error"},
			retryable: true,
			message:   "rate limited",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			v := Classify(tt.err)
			if v.Retryable != tt.retryable {
				t.Errorf("Retryable = %v, want %v", v.Retryable, tt.retryable)
			}
			if tt.retryable && v.Message != tt.message {
				t.Errorf("Message = %q, want %q", v.Message, tt.message)
			}
		})
	}
}

// TestPolicyDelayFor checks that a provider's retry-after headers lengthen
// the policy's delay, up to MaxRetryAfter, but never shorten it.
func TestPolicyDelayFor(t *testing.T) {
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	limited := func(h http.Header) error {
		return fmt.Errorf("stream: %w", &ResponseError{Provider: "provider-b", StatusCode: 429, ResponseHeaders: h})
	}
	p := Policy{Delay: 3 * time.Second}
	for _, tt := range []struct {
		name string
		err  error
		want time.Duration
	}{
		{name: "network error", err: &RequestError{Err: errors.New("reset")}, want: 3 * time.Second},
		{name: "no header", err: limited(nil), want: 3 * time.Second},
		{name: "seconds", err: limited(http.Header{"Retry-After": {"20"}}), want: 20 * time.Second},
		{name: "shorter than policy", err: limited(http.Header{"Retry-After": {"1"}}), want: 3 * time.Second},
		{name: "milliseconds", err: limited(http.Header{"Retry-After-Ms": {"7500"}, "Retry-After": {"8"}}), want: 7500 * time.Millisecond},
		{name: "http date", err: limited(http.Header{"Retry-After": {now.Add(30 * time.Second).Format(http.TimeFormat)}}), want: 30 * time.Second},
		{name: "capped", err: limited(http.Header{"Retry-After": {"3600"}}), want: MaxRetryAfter},
		{name: "garbage", err: limited(http.Header{"Retry-After": {"soon"}}), want: 3 * time.Second},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := p.DelayFor(tt.err, now); got != tt.want {
				t.Errorf("DelayFor = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPolicy(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		p := Policy{}
		if got := p.EffectiveMaxAttempts(); got != DefaultRetryMaxAttempts {
			t.Errorf("EffectiveMaxAttempts() = %d, want %d", got, DefaultRetryMaxAttempts)
		}
		if got := p.EffectiveDelay(); got != DefaultRetryDelay {
			t.Errorf("EffectiveDelay() = %v, want %v", got, DefaultRetryDelay)
		}
	})

	t.Run("custom", func(t *testing.T) {
		p := Policy{MaxAttempts: 3, Delay: 1_000_000_000}
		if got := p.EffectiveMaxAttempts(); got != 3 {
			t.Errorf("EffectiveMaxAttempts() = %d, want 3", got)
		}
		if got := p.EffectiveDelay(); got != 1_000_000_000 {
			t.Errorf("EffectiveDelay() = %v, want 1s", got)
		}
	})
}
