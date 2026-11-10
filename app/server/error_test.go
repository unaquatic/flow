package server

import (
	"bytes"
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"net/http"
	"strconv"
	"testing"

	"github.com/unaquatic/flow/app"
)

// TestErrorResponses checks that requests the API refuses, a path or a
// method it does not serve among them, are answered with an ErrorResponse
// whose code tells the failure apart, and with the status it maps to.
func TestErrorResponses(t *testing.T) {
	ts := newTestServer(t)
	for _, tt := range []struct {
		method, path string
		body         any
		wantStatus   int
		wantCode     app.ErrorCode
	}{
		{"GET", "/nope", nil, http.StatusNotFound, app.CodeRouteNotFound},
		{"DELETE", "/health", nil, http.StatusMethodNotAllowed, app.CodeMethodNotAllowed},
		{"GET", "/session/ses_missing", nil, http.StatusNotFound, app.CodeSessionNotFound},
		{"PATCH", "/session/ses_missing", SessionUpdateRequest{}, http.StatusNotFound, app.CodeSessionNotFound},
		{"POST", "/session/ses_missing/message", SendRequest{}, http.StatusBadRequest, app.CodeInvalidRequest},
		{"POST", "/session/bad.id/message", SendRequest{Text: "hi"}, http.StatusBadRequest, app.CodeInvalidSessionID},
		{"POST", "/session/ses_missing/undo", nil, http.StatusConflict, app.CodeNothingToUndo},
		{"POST", "/session/ses_missing/continue", nil, http.StatusConflict, app.CodeNothingToContinue},
		{"POST", "/session/ses_missing/navigate", NavigateRequest{}, http.StatusBadRequest, app.CodeInvalidRequest},
		{"PATCH", "/config", map[string]string{"theme": "dark"}, http.StatusBadRequest, app.CodeInvalidRequest},
		{"PATCH", "/config", ConfigUpdate{DefaultModel: new("")}, http.StatusBadRequest, app.CodeInvalidModel},
		{"GET", "/model/nope/model-x", nil, http.StatusNotFound, app.CodeProviderNotFound},
		{"DELETE", "/auth/provider-a/accounts/not-a-uuid", nil, http.StatusBadRequest, app.CodeInvalidAccountID},
		{"POST", "/auth/provider-a/accounts", NewAccountRequest{}, http.StatusBadRequest, app.CodeInvalidRequest},
		{"POST", "/auth/provider-a/accounts/00000000-0000-0000-0000-000000000001/code", CodeRequest{}, http.StatusBadRequest, app.CodeInvalidRequest},
	} {
		status, got := doError(t, tt.method, ts.URL+tt.path, tt.body)
		if status != tt.wantStatus || got.Code != tt.wantCode || got.Message == "" {
			t.Errorf("%s %s = %d %+v, want %d %s with a message", tt.method, tt.path, status, got, tt.wantStatus, tt.wantCode)
		}
	}
}

// TestMethodNotAllowedHeader checks that a method a path does not serve is
// answered with the methods it does in the Allow header, as the mux answers.
func TestMethodNotAllowedHeader(t *testing.T) {
	ts := newTestServer(t)
	req, err := http.NewRequestWithContext(t.Context(), "DELETE", ts.URL+"/config", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if got := resp.Header.Get("Allow"); got != "GET, HEAD, PATCH" {
		t.Errorf("DELETE /config: Allow %q, want GET, HEAD, PATCH", got)
	}
}

// TestErrorStatus checks that every error code app declares has a status of
// its own, so that a new code is not answered as the server's own failure,
// which only app.CodeUnknown is.
func TestErrorStatus(t *testing.T) {
	f, err := parser.ParseFile(token.NewFileSet(), "../error.go", nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatal(err)
	}
	var codes []app.ErrorCode
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs := spec.(*ast.ValueSpec)
			if typ, ok := vs.Type.(*ast.Ident); !ok || typ.Name != "ErrorCode" {
				continue
			}
			for _, v := range vs.Values {
				code, err := strconv.Unquote(v.(*ast.BasicLit).Value)
				if err != nil {
					t.Fatal(err)
				}
				codes = append(codes, app.ErrorCode(code))
			}
		}
	}
	if len(codes) == 0 {
		t.Fatal("found no error codes in app/error.go")
	}
	for _, code := range codes {
		_, listed := codeStatus[code]
		if code == app.CodeUnknown {
			listed = !listed
		}
		if !listed {
			t.Errorf("ErrorStatus(%s) = %d, want a status of its own", code, ErrorStatus(code))
		}
	}
	if got := ErrorStatus(app.CodeUnknown); got != http.StatusInternalServerError {
		t.Errorf("ErrorStatus(%s) = %d, want 500", app.CodeUnknown, got)
	}
}

// TestRunModelRefused checks that a prompt or a continue naming a model that
// cannot answer is refused before it stores anything, after the checks of
// the session itself, and that a continue reads the model from a body sent
// without its length.
func TestRunModelRefused(t *testing.T) {
	ts, a := newTestServerApp(t)
	sess := createSession(t, ts)
	base := ts.URL + "/session/" + string(sess.ID)
	for _, tt := range []struct {
		name     string
		path     string
		body     any
		wantCode app.ErrorCode
	}{
		{"malformed model", "/message", SendRequest{Text: "hi", Model: "model-x"}, app.CodeInvalidModel},
		{"unknown provider", "/message", SendRequest{Text: "hi", Model: "nope/model-x"}, app.CodeProviderNotFound},
		{"streamed, unknown provider", "/message", SendRequest{Text: "hi", Model: "nope/model-x", Stream: true}, app.CodeProviderNotFound},
	} {
		if status, got := doError(t, "POST", base+tt.path, tt.body); got.Code != tt.wantCode || status != ErrorStatus(tt.wantCode) {
			t.Errorf("%s: %d %+v, want %s", tt.name, status, got, tt.wantCode)
		}
	}
	var msgs []json.RawMessage
	if status := doJSON(t, "GET", base+"/message", nil, &msgs); status != http.StatusOK || len(msgs) != 0 {
		t.Errorf("messages after refused prompts = %d with status %d, want none", len(msgs), status)
	}

	// A continue's body sent in chunks, with no length, is read all the same.
	appendMessages(t, a, sess.ID, promptMsg("u1", "hi"))
	body, err := json.Marshal(ContinueRequest{Model: "nope/model-x"})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(t.Context(), "POST", base+"/continue", io.MultiReader(bytes.NewReader(body)))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("chunked continue naming an unknown provider: status %d, want 404", resp.StatusCode)
	}

	// A session another operation holds is reported busy, whatever the model.
	op, _, err := a.Claim(t.Context(), sess.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Release(sess.ID, op)
	if status, got := doError(t, "POST", base+"/message", SendRequest{Text: "hi", Model: "nope/model-x"}); got.Code != app.CodeSessionBusy {
		t.Errorf("busy session, unknown provider: %d %+v, want %s", status, got, app.CodeSessionBusy)
	}
}
