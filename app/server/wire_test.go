package server

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/session"
	"github.com/unaquatic/flow/app"
)

// update rewrites the goldens of the wire format, as go test -update does.
var update = flag.Bool("update", false, "rewrite the goldens of the wire format")

// assertWireGolden checks that got is testdata/name, or rewrites the file
// with -update.
func assertWireGolden(t *testing.T, name, got string) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Errorf("%s =\n%s\nwant\n%s", name, got, want)
	}
}

// testListing returns the listing of one provider with a model that takes
// reasoning efforts and one that does not.
func testListing() app.ModelListing {
	return app.NewModelListing("provider-a", []agent.Listing{{Model: "model-x"}, {Model: "model-y", Unlisted: true}})
}

// providerRefusal returns the error response of a provider that is rate
// limited.
func providerRefusal() error {
	return &agent.ResponseError{Provider: "provider-a", StatusCode: http.StatusTooManyRequests, Message: "slow down",
		ResponseBody: `{"error":{"message":"slow down"}}`}
}

// TestWireBodies checks that the response bodies that carry what the app
// returns are written as testdata/wire_bodies.golden records them, one per
// line after the request that answers with it. The events the API streams
// are the app's, which app/testdata/events.golden records.
func TestWireBodies(t *testing.T) {
	created := time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)
	updated := created.Add(time.Minute)
	fork := app.Fork{Parent: "msg_0", Branches: []app.Branch{
		{MessageID: "msg_1", Position: 1, Summary: "first", Messages: 2, CreatedAt: created},
		{MessageID: "msg_2", Position: 2, Summary: "second", Messages: 1, CreatedAt: created, Active: true},
	}}
	sess := session.Session{ID: "ses_1", Title: "Divide by zero", CreatedAt: created, UpdatedAt: updated, Head: session.Head{ID: "msg_2", Forward: "msg_3"}}
	var got strings.Builder
	for _, body := range []struct {
		name string
		v    any
	}{
		{"GET /model", []app.ModelListing{testListing()}},
		{"GET /model none", []app.ModelListing(nil)},
		{"GET /session", []session.Session{sess}},
		{"GET /session/{id} untitled", session.Session{ID: "ses_2", CreatedAt: created, UpdatedAt: created}},
		{"GET /session/{id}/branch", fork},
		{"GET /session/{id}/branch at the root", app.Fork{Branches: fork.Branches}},
		{"POST /session/{id}/undo", CheckoutResponse{Files: []app.FileDiff{
			{File: "calc.go", Additions: 3, Deletions: 1, Status: "modified"},
			{File: "old.go", Deletions: 12, Status: "deleted"},
		}, Checkout: "c1d2e3f4a5b6c7d8e9f0a1b2c3d4e5f6a7b8c9d0"}},
		{"POST /session/{id}/undo nothing checked out", CheckoutResponse{Files: []app.FileDiff{}}},
		{"error", ErrorResponse{Error: app.ErrorOf(app.ErrBusy)}},
		{"error from a provider", ErrorResponse{Error: app.ErrorOf(fmt.Errorf("generate: %w", providerRefusal()))}},
		{"POST /generate generate.failed", GenerateFailed{Error: app.ErrorOf(providerRefusal())}},
	} {
		data, err := json.Marshal(body.v)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&got, "%s %s\n", body.name, data)
	}
	assertWireGolden(t, "wire_bodies.golden", got.String())
}

// TestEmptySessionList checks that GET /session answers an empty list, not
// null, before any session is stored.
func TestEmptySessionList(t *testing.T) {
	ts := newTestServer(t)
	var body json.RawMessage
	if status := doJSON(t, "GET", ts.URL+"/session", nil, &body); status != http.StatusOK || string(body) != "[]" {
		t.Errorf("GET /session = %d %s, want 200 []", status, body)
	}
}
