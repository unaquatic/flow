package app

import (
	"errors"
	"unicode/utf8"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/extension"
)

// ErrorCode identifies what kind of failure an Error is, so that a client
// can tell failures apart without reading their messages. The codes are part
// of the HTTP API: new ones may be added, and a client takes a code it does
// not know as CodeUnknown.
type ErrorCode string

// Error codes of failures of the app's operations, which ErrorOf finds.
const (
	// CodeUnknown identifies a failure of no other kind.
	CodeUnknown ErrorCode = "unknown"
	// CodeSessionNotFound reports that no such session is stored.
	CodeSessionNotFound ErrorCode = "session_not_found"
	// CodeInvalidSessionID reports a session ID that cannot name a session,
	// as ErrInvalidSessionID does.
	CodeInvalidSessionID ErrorCode = "invalid_session_id"
	// CodeMessageNotFound reports that the session has no such message.
	CodeMessageNotFound ErrorCode = "message_not_found"
	// CodeSessionBusy reports that the session has an operation in
	// progress, as ErrBusy does.
	CodeSessionBusy ErrorCode = "session_busy"
	// CodeSessionLocked reports that another process is changing the
	// session, as ErrSessionBusy does.
	CodeSessionLocked ErrorCode = "session_locked"
	// CodeClosed reports that the app is closed, as ErrClosed does.
	CodeClosed ErrorCode = "app_closed"
	// CodeNothingToUndo reports that the session has no prompt to undo.
	CodeNothingToUndo ErrorCode = "nothing_to_undo"
	// CodeNothingToRedo reports that the session has no prompt to redo.
	CodeNothingToRedo ErrorCode = "nothing_to_redo"
	// CodeNothingToContinue reports that the transcript is empty.
	CodeNothingToContinue ErrorCode = "nothing_to_continue"
	// CodeAlreadyThere reports a move that would leave the session where it
	// is, as ErrAlreadyThere does.
	CodeAlreadyThere ErrorCode = "already_there"
	// CodeNoBranches reports that the session's history has no fork.
	CodeNoBranches ErrorCode = "no_branches"
	// CodeRefused reports an operation an extension refused.
	CodeRefused ErrorCode = "refused"

	// CodeInvalidModel reports a model that is not written as
	// "provider/model", as ErrModelFormat does.
	CodeInvalidModel ErrorCode = "invalid_model"
	// CodeModelNotFound reports a model, or a variant of one, that the
	// catalog does not list.
	CodeModelNotFound ErrorCode = "model_not_found"
	// CodeProviderNotFound reports a provider that is not registered.
	CodeProviderNotFound ErrorCode = "provider_not_found"
	// CodeFormatNotSupported reports a model whose request format flow
	// cannot send.
	CodeFormatNotSupported ErrorCode = "format_not_supported"
	// CodeMissingVariable reports an environment variable that a provider
	// needs and that is not set.
	CodeMissingVariable ErrorCode = "missing_variable"
	// CodeNotAuthenticated reports a provider with no account to send a
	// request with.
	CodeNotAuthenticated ErrorCode = "not_authenticated"
	// CodeProviderError reports a provider that failed, or answered with an
	// error response, whose provider, status and body the Error holds.
	CodeProviderError ErrorCode = "provider_error"
	// CodeCatalogUnavailable reports that the model catalog could not be
	// downloaded.
	CodeCatalogUnavailable ErrorCode = "catalog_unavailable"

	// CodeAccountNotFound reports that the provider has no such account.
	CodeAccountNotFound ErrorCode = "account_not_found"
	// CodeInvalidAccountID reports an account ID that is not a UUID.
	CodeInvalidAccountID ErrorCode = "invalid_account_id"
	// CodeEmptyKey reports an empty API key.
	CodeEmptyKey ErrorCode = "empty_key"
	// CodeReadOnlyAccount reports a change to the account that the
	// configuration or environment sets.
	CodeReadOnlyAccount ErrorCode = "read_only_account"
	// CodeInvalidLoginCode reports a pasted authorization result that is
	// malformed, which can be corrected and sent again.
	CodeInvalidLoginCode ErrorCode = "invalid_login_code"
	// CodeLoginFailed reports a login that the provider did not complete.
	CodeLoginFailed ErrorCode = "login_failed"
)

// Error codes the HTTP API answers with for requests it cannot take, which
// ErrorOf never returns.
const (
	// CodeInvalidRequest reports a request body or query that is malformed
	// or misses a field the request needs.
	CodeInvalidRequest ErrorCode = "invalid_request"
	// CodeRouteNotFound reports a path the API does not serve.
	CodeRouteNotFound ErrorCode = "route_not_found"
	// CodeMethodNotAllowed reports a method the API does not serve at the
	// path.
	CodeMethodNotAllowed ErrorCode = "method_not_allowed"
	// CodeUnauthorized reports a request without the credentials the server
	// asks for.
	CodeUnauthorized ErrorCode = "unauthorized"
	// CodeOriginNotAllowed reports a request from a browser page whose
	// origin may not call the API.
	CodeOriginNotAllowed ErrorCode = "origin_not_allowed"
	// CodeHostNotAllowed reports a request to a server on the loopback
	// interface that names another host, as a page that rebinds its own
	// domain name to the loopback address sends.
	CodeHostNotAllowed ErrorCode = "host_not_allowed"
)

// Error is the common shape of failures that the app reports: in the events
// of runs and logins, and in the bodies of the HTTP API's error responses.
type Error struct {
	// Code identifies the kind of failure.
	Code ErrorCode `json:"code"`
	// Message contains the failure detail.
	Message string `json:"message"`
	// Provider names the provider that answered with an error response, or
	// is empty for a failure of another kind.
	Provider agent.ProviderID `json:"provider,omitempty"`
	// Status is the HTTP status of the provider's error response, or zero.
	Status int `json:"status,omitempty"`
	// Body is the body of the provider's error response, as it sent it,
	// such as a JSON error object, or is empty.
	Body string `json:"body,omitempty"`
}

// NewError returns the failure of code with message.
func NewError(code ErrorCode, message string) Error {
	return Error{Code: code, Message: message}
}

// MaxErrorBody is how many bytes of a provider's error response body an
// Error holds, which is ample for an error object, while an error page,
// which some gateways answer with, is cut short.
const MaxErrorBody = 16 << 10

// errorCodes maps the errors of the app, and of the packages it runs, onto
// their codes. ErrorOf takes the first that err wraps.
var errorCodes = []struct {
	err  error
	code ErrorCode
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
	{agent.ErrNoAuthCode, CodeLoginFailed},
	{agent.ErrDeviceCodeExpired, CodeLoginFailed},
	{agent.ErrTokenEndpoint, CodeLoginFailed},
}

// ErrorOf returns the failure err is, whose Message is err's, and whose Code
// is that of the first error of the app's that err wraps, or
// CodeProviderError when err is a provider's failed request or response, or
// else CodeUnknown. When err is a provider's error response, as an
// *agent.ResponseError, it also holds the provider, the status and the body
// it answered with, cut to MaxErrorBody bytes, at a rune boundary.
//
// A session that is not stored is reported with an error wrapping
// fs.ErrNotExist, which ErrorOf leaves as CodeUnknown, since a run can fail
// with it for a file of its own; a caller that asked for a session reports
// it as CodeSessionNotFound.
func ErrorOf(err error) Error {
	e := NewError(codeOf(err), err.Error())
	if resp, ok := errors.AsType[*agent.ResponseError](err); ok {
		e.Provider, e.Status, e.Body = agent.ProviderID(resp.Provider), resp.StatusCode, cutBody(resp.ResponseBody, MaxErrorBody)
	}
	return e
}

// codeOf returns the code of err, as ErrorOf describes it.
func codeOf(err error) ErrorCode {
	for _, c := range errorCodes {
		if errors.Is(err, c.err) {
			return c.code
		}
	}
	if _, ok := errors.AsType[*agent.ResponseError](err); ok {
		return CodeProviderError
	}
	if _, ok := errors.AsType[*agent.RequestError](err); ok {
		return CodeProviderError
	}
	if _, ok := errors.AsType[*agent.ResponseParseError](err); ok {
		return CodeProviderError
	}
	return CodeUnknown
}

// cutBody returns body, or its first n bytes or fewer, ending at a rune
// boundary, when it is longer.
func cutBody(body string, n int) string {
	if len(body) <= n {
		return body
	}
	for n > 0 && !utf8.RuneStart(body[n]) {
		n--
	}
	return body[:n]
}
