package agent

import (
	"context"
	"errors"
	"fmt"
	"net/http"
)

// CredentialKind identifies how a credential is sent to a provider.
type CredentialKind string

const (
	// CredentialAPIKey identifies a static API key.
	CredentialAPIKey CredentialKind = "api_key"
	// CredentialOAuth identifies an OAuth access token.
	CredentialOAuth CredentialKind = "oauth"
)

// Authorization is a credential resolved for one request.
type Authorization struct {
	// Kind selects how Token is sent.
	Kind CredentialKind
	// Token is the API key or OAuth access token.
	Token string
}

// Authorizer resolves the credential of one provider's requests. A provider
// asks it for every request instead of holding a credential.
type Authorizer interface {
	// Authorize returns the credential for one request. A non-nil rejected
	// is a credential the provider refused as unauthorized; Authorize then
	// returns its replacement.
	Authorize(ctx context.Context, rejected *Authorization) (Authorization, error)
}

// Authorized calls do with a credential from az. When the provider rejects an
// OAuth token with 401 Unauthorized, it asks az to replace the token and calls
// do once more. Other failures, and a rejected API key, are returned as they
// are.
func Authorized[T any](ctx context.Context, az Authorizer, do func(Authorization) (T, error)) (T, error) {
	var zero T
	a, err := az.Authorize(ctx, nil)
	if err != nil {
		return zero, err
	}
	v, err := do(a)
	if a.Kind != CredentialOAuth || !IsUnauthorized(err) {
		return v, err
	}
	replaced, replaceErr := az.Authorize(ctx, &a)
	if replaceErr != nil {
		return zero, errors.Join(err, fmt.Errorf("replace rejected token: %w", replaceErr))
	}
	return do(replaced)
}

// IsUnauthorized reports whether err is a 401 Unauthorized provider response.
func IsUnauthorized(err error) bool {
	respErr, ok := errors.AsType[*ResponseError](err)
	return ok && respErr.StatusCode == http.StatusUnauthorized
}
