package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/agent/session"
)

// update rewrites testdata/events.golden, as go test -update does.
var update = flag.Bool("update", false, "rewrite testdata/events.golden")

// eventCase is an event, and the type it has.
type eventCase struct {
	event Event
	typ   EventType
}

// testEvents returns every event the app publishes, in each form its JSON
// takes, such as with and without an error. A new event needs a line here,
// which TestEventContract requires, and its JSON in testdata/events.golden,
// which go test -update writes.
func testEvents() []eventCase {
	base := EventBase{SessionID: "ses_1", Timestamp: time.Date(2026, 10, 30, 1, 2, 3, 0, time.UTC)}
	parent := message.ID("msg_0")
	parts := []message.ContentPart{
		message.TextPart{ID: "p1", Text: "hi"},
		message.ToolPart{ID: "p2", CallID: "c1", Name: "bash", State: message.ToolStateCompleted{Input: json.RawMessage(`{"command":"ls"}`), Output: "a.go"}},
	}
	full := message.Content{ID: "msg_1", ParentID: &parent, Role: message.RoleAgent, Parts: parts, Model: "test/model", CreatedAt: time.Date(2026, 10, 30, 1, 2, 4, 0, time.UTC)}
	root := message.Content{ID: "msg_0", Role: message.RoleUser, Parts: []message.ContentPart{message.TextPart{ID: "p0", Text: "q"}}, Model: "test/model", CreatedAt: time.Date(2026, 10, 30, 1, 2, 2, 0, time.UTC)}
	at := session.Position{Index: 2, Of: 3}
	failed := NewUnknownError("boom")
	change := PartChange{EventBase: base, MessageID: "msg_1", Part: parts[1]}
	listing := NewModelListing("provider-a", []agent.Listing{{Model: "model-x"}, {Model: "model-y", Unlisted: true}})
	return []eventCase{
		{EventMessageStarted{EventBase: base, Message: Message{ID: "msg_1", Role: message.RoleAgent, Model: "test/model"}}, TypeMessageStarted},
		{EventPartStarted{change}, TypePartStarted},
		{EventPartUpdated{change}, TypePartUpdated},
		{EventPartEnded{change}, TypePartEnded},
		{EventMessageEnded{EventBase: base, Message: Message{Content: full}, Finish: agent.StopEndTurn}, TypeMessageEnded},
		{EventMessageEnded{EventBase: base, Message: Message{Content: full, Branch: at}, Stopped: true, Error: &failed}, TypeMessageEnded},
		{EventAgentStarted{EventBase: base}, TypeAgentStarted},
		{EventAgentEnded{EventBase: base}, TypeAgentEnded},
		{EventAgentEnded{EventBase: base, Error: &failed}, TypeAgentEnded},
		{EventAgentRetrying{EventBase: base, MessageID: "msg_1", Attempt: 1, MaxAttempts: 5, RetryAt: base.Timestamp.Add(3 * time.Second),
			Reason: "rate limited", Error: NewUnknownError("provider-a: 429 Too Many Requests")}, TypeAgentRetrying},
		{EventSessionLoaded{EventBase: base, Messages: []Message{{Content: root}, {Content: full, Branch: at}}}, TypeSessionLoaded},
		{EventSessionLoaded{EventBase: base}, TypeSessionLoaded},
		{EventSessionUpdated{EventBase: base, Title: "fix the tests", CreatedAt: root.CreatedAt, UpdatedAt: full.CreatedAt}, TypeSessionUpdated},
		{EventSessionUpdated{EventBase: base, CreatedAt: root.CreatedAt, UpdatedAt: root.CreatedAt}, TypeSessionUpdated},
		{EventSessionDeleted{EventBase: base}, TypeSessionDeleted},
		{EventLoginStarted{EventBase: base, Provider: "provider-a", Account: "acc_1", Method: "browser"}, TypeLoginStarted},
		{EventLoginPrompt{EventBase: base, Provider: "provider-a", Account: "acc_1", URL: "https://login.example/x", Code: "ABCD"}, TypeLoginPrompt},
		{EventLoginPrompt{EventBase: base, Provider: "provider-a", Account: "acc_1", URL: "https://login.example/x"}, TypeLoginPrompt},
		{EventLoginSucceeded{EventBase: base, Provider: "provider-a", Account: "acc_1"}, TypeLoginSucceeded},
		{EventLoginFailed{EventBase: base, Provider: "provider-a", Account: "acc_1", Error: failed}, TypeLoginFailed},
		{EventAccountsChanged{EventBase: base, Provider: "provider-a", Active: "acc_1"}, TypeAccountsChanged},
		{EventModelsListed{EventBase: base, Listings: []ModelListing{listing}}, TypeModelsListed},
		{EventModelsListed{EventBase: base, Error: &failed}, TypeModelsListed},
		{EventModelsRefreshed{EventBase: base}, TypeModelsRefreshed},
	}
}

// TestEventContract checks that each event has its type, that no two events
// share one, and that testEvents has every event the package declares.
func TestEventContract(t *testing.T) {
	covered := make(map[string]bool)
	owners := make(map[EventType]reflect.Type)
	for _, tt := range testEvents() {
		rt := reflect.TypeOf(tt.event)
		covered[rt.Name()] = true
		if got := tt.event.EventType(); got != tt.typ {
			t.Errorf("%v EventType() = %q, want %q", rt, got, tt.typ)
		}
		if owner, ok := owners[tt.typ]; ok && owner != rt {
			t.Errorf("%v and %v share event type %q", owner, rt, tt.typ)
		}
		owners[tt.typ] = rt
	}
	for _, name := range declaredEvents(t) {
		if !covered[name] {
			t.Errorf("%s has no line in testEvents", name)
		}
	}
}

// TestEventJSON checks that each event of testEvents encodes as
// testdata/events.golden records it, one per line after its type, as the
// HTTP API streams it.
func TestEventJSON(t *testing.T) {
	var got strings.Builder
	for _, tt := range testEvents() {
		data, err := json.Marshal(tt.event)
		if err != nil {
			t.Fatalf("encode %T: %v", tt.event, err)
		}
		fmt.Fprintf(&got, "%s %s\n", tt.event.EventType(), data)
	}
	assertGolden(t, "testdata/events.golden", got.String())
}

// declaredEvents returns the name of every type that the package's source
// declares an EventType method on.
func declaredEvents(t *testing.T) []string {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	var names []string
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Recv == nil || fn.Name.Name != "EventType" {
				continue
			}
			if recv, ok := fn.Recv.List[0].Type.(*ast.Ident); ok {
				names = append(names, recv.Name)
			}
		}
	}
	if len(names) == 0 {
		t.Fatal("found no events in the package source")
	}
	return names
}

// assertGolden checks that got is the file at path, or rewrites the file
// with -update.
func assertGolden(t *testing.T, path, got string) {
	t.Helper()
	if *update {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
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
		t.Errorf("%s =\n%s\nwant\n%s", path, got, want)
	}
}

func TestNewUnknownError(t *testing.T) {
	err := NewUnknownError("provider failed")
	if err.Type != ErrorTypeUnknown {
		t.Fatalf("error type = %q, want %q", err.Type, ErrorTypeUnknown)
	}
	if err.Message != "provider failed" {
		t.Fatalf("error message = %q", err.Message)
	}
}

// TestErrorOf checks that a provider's error response, wrapped or not, keeps
// its provider, status and body, and that another error keeps its message
// alone.
func TestErrorOf(t *testing.T) {
	resp := &agent.ResponseError{Provider: "provider-a", StatusCode: 400, Message: "bad", ResponseBody: `{"error":{"message":"bad"}}`}
	got := ErrorOf(fmt.Errorf("turn: %w", resp))
	want := UnknownError{Type: ErrorTypeUnknown, Message: "turn: " + resp.Error(), Provider: "provider-a", Status: 400, Body: resp.ResponseBody}
	if got != want {
		t.Errorf("ErrorOf(response error) = %+v, want %+v", got, want)
	}
	if got, want := ErrorOf(errors.New("boom")), NewUnknownError("boom"); got != want {
		t.Errorf("ErrorOf(boom) = %+v, want %+v", got, want)
	}

	// A body longer than MaxErrorBody is cut short at a rune boundary: the
	// two-byte é that would straddle the cut is left out whole.
	long := strings.Repeat("a", MaxErrorBody-1) + "é" + "tail"
	body := ErrorOf(&agent.ResponseError{Provider: "provider-a", StatusCode: 502, ResponseBody: long}).Body
	if want := strings.Repeat("a", MaxErrorBody-1); body != want {
		t.Errorf("ErrorOf(long body).Body is %d bytes, want the %d before é", len(body), len(want))
	}
}

// TestAgentEvent checks that each event of an agent run becomes the app event
// that clients read, stamped with the base it is given, with its message, and
// the error that ended a message described.
func TestAgentEvent(t *testing.T) {
	base := EventBase{SessionID: "ses_1", Timestamp: time.Unix(1, 0).UTC()}
	parent := message.ID("msg_0")
	msg := message.Content{ID: "msg_1", ParentID: &parent, Role: message.RoleAgent,
		Parts: []message.ContentPart{message.TextPart{ID: "p1", Text: "hi"}}, Model: "test/model", CreatedAt: time.Unix(2, 0).UTC()}
	change := agent.PartChange{MessageID: "msg_1", Part: message.TextPart{ID: "p1", Text: "hi"}}
	want := PartChange{EventBase: base, MessageID: "msg_1", Part: change.Part}
	failed := NewUnknownError("boom")
	for _, tt := range []struct {
		in   agent.Event
		want Event
	}{
		{agent.MessageStarted{Message: msg}, EventMessageStarted{EventBase: base, Message: Message{Content: msg}}},
		{agent.PartStarted{PartChange: change}, EventPartStarted{want}},
		{agent.PartUpdated{PartChange: change}, EventPartUpdated{want}},
		{agent.PartEnded{PartChange: change}, EventPartEnded{want}},
		{agent.MessageEnded{Message: msg, Finish: agent.StopEndTurn, Stopped: true},
			EventMessageEnded{EventBase: base, Message: Message{Content: msg}, Finish: agent.StopEndTurn, Stopped: true}},
		{agent.MessageEnded{Message: msg, Err: errors.New("boom")},
			EventMessageEnded{EventBase: base, Message: Message{Content: msg}, Error: &failed}},
		{agent.Retrying{MessageID: "msg_1", Attempt: 2, MaxAttempts: 5, Delay: 3 * time.Second, Reason: "rate limited", Err: errors.New("boom")},
			EventAgentRetrying{EventBase: base, MessageID: "msg_1", Attempt: 2, MaxAttempts: 5, RetryAt: base.Timestamp.Add(3 * time.Second),
				Reason: "rate limited", Error: failed}},
	} {
		if got := AgentEvent(base, tt.in); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("AgentEvent(%T) = %+v, want %+v", tt.in, got, tt.want)
		}
	}
}
