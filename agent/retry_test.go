package agent

import (
	"context"
	"errors"
	"fmt"
	"testing"
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
			err:       &ResponseError{Provider: "anthropic", StatusCode: 500, Message: "Internal Server Error"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "502 bad gateway",
			err:       &ResponseError{Provider: "openai", StatusCode: 502, Message: "Bad Gateway"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "503 service unavailable",
			err:       &ResponseError{Provider: "openai", StatusCode: 503, Message: "Service Unavailable"},
			retryable: true,
			message:   "server error",
		},
		{
			name:      "529 overloaded",
			err:       &ResponseError{Provider: "anthropic", StatusCode: 529, Message: "Overloaded"},
			retryable: true,
			message:   "provider overloaded",
		},
		{
			name:      "429 rate limited",
			err:       &ResponseError{Provider: "anthropic", StatusCode: 429, Message: "Rate limit reached"},
			retryable: true,
			message:   "rate limited",
		},
		{
			name: "401 unauthorized",
			err:  &ResponseError{Provider: "openai", StatusCode: 401, Message: "Invalid API key"},
		},
		{
			name: "403 forbidden",
			err:  &ResponseError{Provider: "anthropic", StatusCode: 403, Message: "Permission denied"},
		},
		{
			name: "400 bad request",
			err:  &ResponseError{Provider: "anthropic", StatusCode: 400, Message: "Invalid request"},
		},
		{
			name: "404 not found",
			err:  &ResponseError{Provider: "openai", StatusCode: 404, Message: "Model not found"},
		},

		{
			name:      "mid-stream overloaded",
			err:       &ResponseError{Provider: "anthropic", StatusCode: 0, Message: "Overloaded"},
			retryable: true,
			message:   "provider overloaded",
		},
		{
			name:      "mid-stream rate limit",
			err:       &ResponseError{Provider: "openai", StatusCode: 0, Message: "Rate limit reached"},
			retryable: true,
			message:   "rate limited",
		},
		{
			name:      "mid-stream too many requests",
			err:       &ResponseError{Provider: "openai", StatusCode: 0, Message: "Too Many Requests"},
			retryable: true,
			message:   "too many requests",
		},
		{
			name: "mid-stream unrecognized message",
			err:  &ResponseError{Provider: "openai", StatusCode: 0, Message: "something else entirely"},
		},

		{
			name: "body contains rate_limit_error",
			err: &ResponseError{
				Provider:     "anthropic",
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
				Provider:     "anthropic",
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
			name:      "wrapped 500",
			err:       fmt.Errorf("gateway: %w", &ResponseError{Provider: "anthropic", StatusCode: 500, Message: "fail"}),
			retryable: true,
			message:   "server error",
		},

		{
			name: "ErrNotAuthenticated",
			err:  ErrNotAuthenticated,
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
