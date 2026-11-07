package agent

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
)

// fakeAuthorizer hands out current, and replacement for a rejected
// credential.
type fakeAuthorizer struct {
	current     Authorization
	replacement Authorization
	replaceErr  error
	rejected    []Authorization // credentials reported as rejected
}

func (f *fakeAuthorizer) Authorize(_ context.Context, rejected *Authorization) (Authorization, error) {
	if rejected == nil {
		return f.current, nil
	}
	f.rejected = append(f.rejected, *rejected)
	return f.replacement, f.replaceErr
}

func TestAuthorized(t *testing.T) {
	oldToken := Authorization{Kind: CredentialOAuth, Token: "old"}
	newToken := Authorization{Kind: CredentialOAuth, Token: "new"}
	apiKey := Authorization{Kind: CredentialAPIKey, Token: "sk"}
	unauthorized := &ResponseError{StatusCode: http.StatusUnauthorized}
	forbidden := &ResponseError{StatusCode: http.StatusForbidden}
	errReplace := errors.New("refresh token revoked")

	tests := []struct {
		name         string
		current      Authorization
		replaceErr   error
		results      map[string]error // error of do for each token
		wantCalls    []string         // tokens passed to do
		wantReplaced int
		wantErr      []error
	}{
		{
			name:         "accepted",
			current:      oldToken,
			wantCalls:    []string{"old"},
			wantReplaced: 0,
		},
		{
			name:         "replaced after 401",
			current:      oldToken,
			results:      map[string]error{"old": unauthorized},
			wantCalls:    []string{"old", "new"},
			wantReplaced: 1,
		},
		{
			name:         "401 again after replacement is not retried",
			current:      oldToken,
			results:      map[string]error{"old": unauthorized, "new": unauthorized},
			wantCalls:    []string{"old", "new"},
			wantReplaced: 1,
			wantErr:      []error{unauthorized},
		},
		{
			name:         "403 is not replaced",
			current:      oldToken,
			results:      map[string]error{"old": forbidden},
			wantCalls:    []string{"old"},
			wantReplaced: 0,
			wantErr:      []error{forbidden},
		},
		{
			name:         "rejected API key is not replaced",
			current:      apiKey,
			results:      map[string]error{"sk": unauthorized},
			wantCalls:    []string{"sk"},
			wantReplaced: 0,
			wantErr:      []error{unauthorized},
		},
		{
			name:         "failed replacement reports both errors",
			current:      oldToken,
			replaceErr:   errReplace,
			results:      map[string]error{"old": unauthorized},
			wantCalls:    []string{"old"},
			wantReplaced: 1,
			wantErr:      []error{unauthorized, errReplace},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			az := &fakeAuthorizer{current: tt.current, replacement: newToken, replaceErr: tt.replaceErr}
			var calls []string
			got, err := Authorized(t.Context(), az, func(a Authorization) (string, error) {
				calls = append(calls, a.Token)
				if err := tt.results[a.Token]; err != nil {
					return "", err
				}
				return a.Token, nil
			})

			if !slices.Equal(calls, tt.wantCalls) {
				t.Errorf("do called with %q, want %q", calls, tt.wantCalls)
			}
			if len(az.rejected) != tt.wantReplaced {
				t.Errorf("replaced %d times, want %d", len(az.rejected), tt.wantReplaced)
			}
			if len(az.rejected) > 0 && az.rejected[0] != tt.current {
				t.Errorf("reported %+v as rejected, want %+v", az.rejected[0], tt.current)
			}
			for _, want := range tt.wantErr {
				if !errors.Is(err, want) {
					t.Errorf("error = %v, want it to wrap %v", err, want)
				}
			}
			if tt.wantErr == nil {
				if err != nil {
					t.Fatalf("error = %v", err)
				}
				if want := tt.wantCalls[len(tt.wantCalls)-1]; got != want {
					t.Errorf("result = %q, want %q", got, want)
				}
			}
		})
	}
}
