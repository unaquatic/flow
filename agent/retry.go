package agent

import (
	"context"
	"errors"
	"strings"
	"time"
)

const (
	// DefaultRetryMaxAttempts is the default total request count.
	DefaultRetryMaxAttempts = 5
	// DefaultRetryDelay is the default delay between requests.
	DefaultRetryDelay = 3 * time.Second
	// MaxRetryAfter caps the wait a provider's retry-after header can ask
	// for between requests.
	MaxRetryAfter = time.Minute
)

// RetryVerdict describes whether and why an operation should be retried.
type RetryVerdict struct {
	// Retryable reports whether the error is considered transient.
	Retryable bool

	// Message explains a retryable verdict.
	Message string
}

// Classify marks request errors, HTTP 429 and 5xx responses, and recognized
// transient response text as retryable. Context and authentication failures
// are not retryable.
func Classify(err error) RetryVerdict {
	if err == nil {
		return RetryVerdict{}
	}

	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return RetryVerdict{}
	}

	if respErr, ok := errors.AsType[*ResponseError](err); ok {
		return classifyResponse(respErr)
	}

	if _, ok := errors.AsType[*RequestError](err); ok {
		return RetryVerdict{Retryable: true, Message: "network error"}
	}

	return RetryVerdict{}
}

// classifyResponse classifies a provider response error.
func classifyResponse(e *ResponseError) RetryVerdict {
	if e.StatusCode == 401 || e.StatusCode == 403 {
		return RetryVerdict{}
	}

	if e.StatusCode == 429 {
		return RetryVerdict{Retryable: true, Message: "rate limited"}
	}

	if e.StatusCode >= 500 {
		msg := "server error"
		if containsFold(e.Message, "overloaded") {
			msg = "provider overloaded"
		}
		return RetryVerdict{Retryable: true, Message: msg}
	}

	lower := strings.ToLower(e.Message)
	if containsAny(lower, "overloaded", "rate limit", "too many requests", "rate_limit", "exhausted", "unavailable") {
		return retryVerdictFromText(lower)
	}

	if e.ResponseBody != "" {
		bodyLower := strings.ToLower(e.ResponseBody)
		if containsAny(bodyLower, "overloaded", "rate_limit", "too_many_requests", "exhausted", "unavailable") {
			return retryVerdictFromText(bodyLower)
		}
	}

	return RetryVerdict{}
}

// retryVerdictFromText classifies recognized text in a lowercase error.
func retryVerdictFromText(lower string) RetryVerdict {
	switch {
	case strings.Contains(lower, "overloaded"):
		return RetryVerdict{Retryable: true, Message: "provider overloaded"}
	case strings.Contains(lower, "rate limit") || strings.Contains(lower, "rate_limit"):
		return RetryVerdict{Retryable: true, Message: "rate limited"}
	case strings.Contains(lower, "too many requests") || strings.Contains(lower, "too_many_requests"):
		return RetryVerdict{Retryable: true, Message: "too many requests"}
	case strings.Contains(lower, "exhausted"):
		return RetryVerdict{Retryable: true, Message: "provider overloaded"}
	case strings.Contains(lower, "unavailable"):
		return RetryVerdict{Retryable: true, Message: "provider unavailable"}
	default:
		return RetryVerdict{Retryable: true, Message: "transient error"}
	}
}

// containsFold reports whether s contains substr without case sensitivity.
func containsFold(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// containsAny reports whether s contains any of the given substrings.
func containsAny(s string, substrs ...string) bool {
	for _, sub := range substrs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// Policy controls retry behavior for transient provider errors.
type Policy struct {
	// MaxAttempts is the total number of attempts (including the
	// initial one). Zero uses DefaultRetryMaxAttempts.
	MaxAttempts int

	// Delay is the fixed delay between attempts. Zero uses
	// DefaultRetryDelay.
	Delay time.Duration
}

// EffectiveMaxAttempts returns MaxAttempts or the default.
func (p Policy) EffectiveMaxAttempts() int {
	if p.MaxAttempts > 0 {
		return p.MaxAttempts
	}
	return DefaultRetryMaxAttempts
}

// EffectiveDelay returns Delay or the default.
func (p Policy) EffectiveDelay() time.Duration {
	if p.Delay > 0 {
		return p.Delay
	}
	return DefaultRetryDelay
}

// DelayFor returns how long to wait before retrying after err: the
// effective delay, or longer when err is a *ResponseError whose headers ask
// for a longer wait, up to MaxRetryAfter.
func (p Policy) DelayFor(err error, now time.Time) time.Duration {
	delay := p.EffectiveDelay()
	if respErr, ok := errors.AsType[*ResponseError](err); ok {
		delay = max(delay, min(respErr.RetryAfter(now), MaxRetryAfter))
	}
	return delay
}
