package app

import (
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/extension"
)

// TestErrorOf checks that a provider's error response, wrapped or not, keeps
// its provider, status and body, and that another error keeps its message
// alone.
func TestErrorOf(t *testing.T) {
	resp := &agent.ResponseError{Provider: "provider-a", StatusCode: 400, Message: "bad", ResponseBody: `{"error":{"message":"bad"}}`}
	got := ErrorOf(fmt.Errorf("turn: %w", resp))
	want := Error{Code: CodeProviderError, Message: "turn: " + resp.Error(), Provider: "provider-a", Status: 400, Body: resp.ResponseBody}
	if got != want {
		t.Errorf("ErrorOf(response error) = %+v, want %+v", got, want)
	}
	if got, want := ErrorOf(errors.New("boom")), NewError(CodeUnknown, "boom"); got != want {
		t.Errorf("ErrorOf(boom) = %+v, want %+v", got, want)
	}

	// A token endpoint's error response keeps its status and body too, and
	// names no provider, which the login it failed names.
	tok := &agent.TokenEndpointError{StatusCode: 401, Body: `{"error":"invalid_grant"}`}
	got = ErrorOf(fmt.Errorf("token exchange: %w", tok))
	want = Error{Code: CodeLoginFailed, Message: "token exchange: " + tok.Error(), Status: 401, Body: tok.Body}
	if got != want {
		t.Errorf("ErrorOf(token endpoint error) = %+v, want %+v", got, want)
	}

	// A body longer than MaxErrorBody is cut short at a rune boundary: the
	// two-byte é that would straddle the cut is left out whole.
	long := strings.Repeat("a", MaxErrorBody-1) + "é" + "tail"
	body := ErrorOf(&agent.ResponseError{Provider: "provider-a", StatusCode: 502, ResponseBody: long}).Body
	if want := strings.Repeat("a", MaxErrorBody-1); body != want {
		t.Errorf("ErrorOf(long body).Body is %d bytes, want the %d before é", len(body), len(want))
	}
}

// TestErrorOfCode checks that ErrorOf finds the code of each error the app
// reports, through wrapping, and leaves an error it does not know, a missing
// file among them, as CodeUnknown.
func TestErrorOfCode(t *testing.T) {
	for _, tt := range []struct {
		err  error
		want ErrorCode
	}{
		{ErrInvalidSessionID, CodeInvalidSessionID},
		{session.ErrMessageNotFound, CodeMessageNotFound},
		{ErrBusy, CodeSessionBusy},
		{ErrSessionBusy, CodeSessionLocked},
		{ErrClosed, CodeClosed},
		{ErrNothingToUndo, CodeNothingToUndo},
		{ErrNothingToRedo, CodeNothingToRedo},
		{ErrNothingToContinue, CodeNothingToContinue},
		{ErrAlreadyThere, CodeAlreadyThere},
		{ErrNoBranches, CodeNoBranches},
		{extension.ErrRefused, CodeRefused},
		{ErrModelFormat, CodeInvalidModel},
		{ErrNoModelSet, CodeNoModelSet},
		{ErrNoModel, CodeModelNotFound},
		{agent.ErrModelNotFound, CodeModelNotFound},
		{agent.ErrVariantNotFound, CodeModelNotFound},
		{agent.ErrProviderNotRegistered, CodeProviderNotFound},
		{agent.ErrFormatNotSupported, CodeFormatNotSupported},
		{agent.ErrMissingVariable, CodeMissingVariable},
		{agent.ErrNotAuthenticated, CodeNotAuthenticated},
		{gateway.ErrSource, CodeCatalogUnavailable},
		{agent.ErrUnknownAccount, CodeAccountNotFound},
		{agent.ErrInvalidAccountID, CodeInvalidAccountID},
		{agent.ErrEmptyKey, CodeEmptyKey},
		{agent.ErrReadOnlyAccount, CodeReadOnlyAccount},
		{agent.ErrInvalidLoginCode, CodeInvalidLoginCode},
		{agent.ErrTokenEndpoint, CodeLoginFailed},
		{&agent.RequestError{Err: errors.New("EOF")}, CodeProviderError},
		{&agent.ResponseParseError{Err: errors.New("bad json")}, CodeProviderError},
		{fs.ErrNotExist, CodeUnknown},
		{errors.New("boom"), CodeUnknown},
	} {
		wrapped := fmt.Errorf("op: %w", tt.err)
		if got := ErrorOf(wrapped).Code; got != tt.want {
			t.Errorf("ErrorOf(%v).Code = %q, want %q", wrapped, got, tt.want)
		}
	}
}
