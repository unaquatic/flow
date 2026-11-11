package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension"
)

// titleAsk is the message a titler forks the session with.
var titleAsk = agent.NewUserMessage("title this")

// titler is an extension that titles sessions with title, or with what the
// model replies when title is empty, and records the titlings it is asked
// for. When wait is set, it waits for it to close, or for its ctx to end,
// before it answers, and records whether the ctx ended.
type titler struct {
	name  string
	title string
	err   error
	wait  chan struct{}
	// asking, when set, receives each ask as it starts.
	asking chan struct{}

	mu        sync.Mutex
	asks      []extension.Titling
	cancelled []bool
}

func (t *titler) Name() string { return t.name }

func (t *titler) Title(ctx context.Context, tt extension.Titling) (string, error) {
	t.mu.Lock()
	t.asks = append(t.asks, tt)
	t.mu.Unlock()
	if t.asking != nil {
		t.asking <- struct{}{}
	}
	if t.wait != nil {
		select {
		case <-t.wait:
		case <-ctx.Done():
		}
		t.mu.Lock()
		t.cancelled = append(t.cancelled, ctx.Err() != nil)
		t.mu.Unlock()
		if err := ctx.Err(); err != nil {
			return "", err
		}
	}
	if t.err != nil || t.title != "" {
		return t.title, t.err
	}
	resp, err := tt.Fork(ctx, "", titleAsk)
	if err != nil {
		return "", err
	}
	return resp.Message.TextContent(), nil
}

// asked returns the titlings t was asked for.
func (t *titler) asked() []extension.Titling {
	t.mu.Lock()
	defer t.mu.Unlock()
	return slices.Clone(t.asks)
}

// texts returns the text of each of msgs.
func texts(msgs []agent.Message) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.TextContent()
	}
	return out
}

// chatTexts returns the text of each message of msgs after the system
// message they open with, which a session's first prompt stores, or nil
// when they open with none, which no conversation sent from the app does.
func chatTexts(msgs []agent.Message) []string {
	if len(msgs) == 0 || msgs[0].Role != message.RoleSystem {
		return nil
	}
	return texts(msgs[1:])
}

// titleOf returns the stored title of session id.
func titleOf(t *testing.T, a *App, id message.SessionID) string {
	t.Helper()
	stored, err := a.Sessions().Session(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	return stored.Title
}

// sendAndSettle sends text to session id, and waits for its run and its
// titling to end.
func sendAndSettle(t *testing.T, a *App, events <-chan Event, id message.SessionID, input SendInput) []Event {
	t.Helper()
	send(t, a, id, input)
	out := collectUntilIdle(t, events)
	a.wg.Wait()
	for len(events) > 0 {
		out = append(out, <-events)
	}
	return out
}

// TestTitling_TitlesAfterEachPrompt checks that each prompt has the title
// hooks asked, in order, past those that fail or return no title, with the
// prompt, the history through it, the title as it stands, and a Fork that
// reaches the app's models; that the first title is set and published; and
// that a title the session has already is not set again.
func TestTitling_TitlesAfterEachPrompt(t *testing.T) {
	a, _ := newActorTestApp(t, "Greeting the agent")
	failing := &titler{name: "failing", err: errors.New("no model")}
	empty := &titler{name: "empty", title: "  "}
	generating := &titler{name: "generating"}
	last := &titler{name: "last", title: "never"}
	a.extensions = append(a.extensions, failing, empty, generating, last)
	events := a.Subscribe(t.Context())

	const id = "ses_title"
	first := sendAndSettle(t, a, events, id, SendInput{Text: "hi", MessageID: "msg_first"})
	if got := titleOf(t, a, id); got != "Greeting the agent" {
		t.Errorf("title = %q, want the reply the generating hook got", got)
	}
	if !slices.ContainsFunc(eventsOfType[EventSessionUpdated](first), func(u EventSessionUpdated) bool { return u.Title == "Greeting the agent" }) {
		t.Error("no EventSessionUpdated published the title")
	}
	for _, h := range []*titler{failing, empty, generating} {
		asks := h.asked()
		if len(asks) != 1 {
			t.Fatalf("%s was asked %d times, want once", h.name, len(asks))
		}
		tt := asks[0]
		if tt.SessionID != id || tt.MessageID != "msg_first" || tt.Text != "hi" || tt.Model == "" ||
			!slices.Equal(chatTexts(tt.History), []string{"hi"}) || tt.Title != "" {
			t.Errorf("%s was asked for %+v, want the first prompt of an untitled session", h.name, tt)
		}
	}
	if n := len(last.asked()); n != 0 {
		t.Errorf("the hook after the one that titled was asked %d times, want none", n)
	}

	second := sendAndSettle(t, a, events, id, SendInput{Text: "again"})
	asks := generating.asked()
	if len(asks) != 2 {
		t.Fatalf("the generating hook was asked %d times over two prompts, want twice", len(asks))
	}
	if tt := asks[1]; tt.Text != "again" || !slices.Equal(chatTexts(tt.History), []string{"hi", "Greeting the agent", "again"}) || tt.Title != "Greeting the agent" {
		t.Errorf("second titling = %+v, want the history through the second prompt and the current title", tt)
	}
	if n := len(eventsOfType[EventSessionUpdated](second)); n != 0 {
		t.Errorf("the same title was published again %d times, want none", n)
	}
}

// TestTitling_LaterPromptSupersedes checks that a prompt cancels the
// titling an earlier prompt to the session started, whose title is not set.
func TestTitling_LaterPromptSupersedes(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	slow := &titler{name: "slow", title: "from the hook", wait: make(chan struct{}), asking: make(chan struct{}, 2)}
	a.extensions = append(a.extensions, slow)
	events := a.Subscribe(t.Context())

	const id = "ses_supersede"
	send(t, a, id, SendInput{Text: "first"})
	collectUntilIdle(t, events)
	// The first titling reads the session before it asks the hook; a prompt
	// sent before then would end it unasked.
	select {
	case <-slow.asking:
	case <-time.After(10 * time.Second):
		t.Fatal("the first titling never asked the hook")
	}
	send(t, a, id, SendInput{Text: "second"})
	collectUntilIdle(t, events)
	close(slow.wait)
	a.wg.Wait()

	slow.mu.Lock()
	cancelled := slices.Clone(slow.cancelled)
	slow.mu.Unlock()
	if !slices.Equal(cancelled, []bool{true, false}) {
		t.Errorf("titlings cancelled = %v, want the first only", cancelled)
	}
	if got := titleOf(t, a, id); got != "from the hook" {
		t.Errorf("title = %q, want the second titling's", got)
	}
}

// TestTitling_SetsOverATitleSetMeanwhile checks that a hook's title is set
// over one set while the hook wrote it, as over any other.
func TestTitling_SetsOverATitleSetMeanwhile(t *testing.T) {
	a, _ := newActorTestApp(t, "hello")
	slow := &titler{name: "slow", title: "from the hook", wait: make(chan struct{})}
	a.extensions = append(a.extensions, slow)
	events := a.Subscribe(t.Context())

	const id = "ses_meanwhile"
	send(t, a, id, SendInput{Text: "hi"})
	collectUntilIdle(t, events)
	if _, err := a.Session(id).SetTitle("by hand"); err != nil {
		t.Fatal(err)
	}
	close(slow.wait)
	a.wg.Wait()

	if got := titleOf(t, a, id); got != "from the hook" {
		t.Errorf("title = %q, want the hook's", got)
	}
}

// TestTitling_ForksTheSession checks that a hook's fork sends the history
// through the prompt and then its own messages, with the session's cache
// key, as a run sends its requests, and that it stores them and the reply
// as a title fork below the prompt, which the session's branch and tree
// leave out.
func TestTitling_ForksTheSession(t *testing.T) {
	a, gw := newActorTestApp(t, "Greeting the agent")
	a.extensions = append(a.extensions, &titler{name: "forking"})
	events := a.Subscribe(t.Context())

	const id = "ses_fork"
	sendAndSettle(t, a, events, id, SendInput{Text: "hi", MessageID: "msg_first"})

	var forked []agent.Message
	for i, call := range gw.StreamCalls {
		if last := call[len(call)-1]; last.TextContent() == titleAsk.TextContent() {
			forked = call
			if key := gw.StreamCacheKeys[i]; key != id {
				t.Errorf("fork cache key = %q, want the session's", key)
			}
		}
	}
	if !slices.Equal(chatTexts(forked), []string{"hi", "title this"}) {
		t.Errorf("fork sent %q, want the system message, the prompt and the hook's message", texts(forked))
	}

	whole, err := a.Sessions().Branch(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if branch := conversation(t, whole); len(branch) != 2 || branch[0].ID != "msg_first" || branch[1].Role != message.RoleAgent {
		t.Fatalf("branch = %+v; want the system message, the prompt and its reply alone", whole)
	}
	asked, err := a.Sessions().ForkChildren(t.Context(), id, "msg_first", message.TypeTitle)
	if err != nil || len(asked) != 1 || asked[0].Role != message.RoleUser || asked[0].Type != message.TypeTitle {
		t.Fatalf("title forks below the prompt = %+v, %v; want one", asked, err)
	}
	if got := (agent.Message{Content: asked[0].Parts}).TextContent(); got != titleAsk.TextContent() || asked[0].Model == "" {
		t.Errorf("fork's first message = %q of %q, want the hook's message, of the prompt's model", got, asked[0].Model)
	}
	replied, err := a.Sessions().ForkChildren(t.Context(), id, asked[0].ID, message.TypeTitle)
	if err != nil || len(replied) != 1 || replied[0].Role != message.RoleAgent || replied[0].Type != message.TypeTitle {
		t.Fatalf("reply below the fork's message = %+v, %v; want the agent's", replied, err)
	}
	if got := (agent.Message{Content: replied[0].Parts}).TextContent(); got != "Greeting the agent" {
		t.Errorf("fork's reply = %q, want the model's", got)
	}
	all, err := a.Sessions().Messages(t.Context(), id)
	if err != nil || len(all) != 3 {
		t.Errorf("tree holds %d messages, %v; want the branch's three", len(all), err)
	}
	tree, err := a.Session(id).Tree()
	if err != nil {
		t.Fatal(err)
	}
	if len(tree.Messages) != 3 || len(tree.Forks) != 2 || tree.Forks[0].ID != asked[0].ID || tree.Forks[1].ID != replied[0].ID {
		t.Errorf("tree = %d messages and forks %+v; want the branch's three, and the fork's request and reply", len(tree.Messages), tree.Forks)
	}
}
