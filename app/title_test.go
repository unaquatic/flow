package app

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension"
)

// titleSystem is the system message a titler asks for its reply with.
const titleSystem = "title this"

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
	resp, err := tt.Reply(ctx, "", titleSystem)
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
// prompt, the title thread through it, the title as it stands, and a Reply
// that reaches the app's models; that the first title is set and published;
// and that a title the session has already is not set again.
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
			!slices.Equal(tt.Prompts, []string{"hi"}) || tt.Title != "" {
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
	if tt := asks[1]; tt.Text != "again" || !slices.Equal(tt.Prompts, []string{"hi", "again"}) || tt.Title != "Greeting the agent" {
		t.Errorf("second titling = %+v, want both prompts and the current title", tt)
	}
	if n := len(eventsOfType[EventSessionUpdated](second)); n != 0 {
		t.Errorf("the same title was published again %d times, want none", n)
	}
}

// TestTitling_LaterPromptSupersedes checks that a prompt cancels the
// titling an earlier prompt to the session started, whose title is not set,
// and that the later titling still reads the earlier prompt.
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
	if asks := slow.asked(); len(asks) != 2 || !slices.Equal(asks[1].Prompts, []string{"first", "second"}) {
		t.Errorf("titlings = %+v, want the second of both prompts", asks)
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

// titleCalls returns the requests a titler's Reply sent, which open with
// its system message in an instructions section, and their cache keys and tools.
func titleCalls(gw *agenttest.Gateway) (calls [][]agent.Message, keys []string, tools [][]agent.ToolDefinition) {
	for i, call := range gw.StreamCalls {
		if len(call) > 0 && call[0].Role == message.RoleSystem && call[0].TextContent() == Section(InstructionsTag, titleSystem) {
			calls = append(calls, call)
			keys = append(keys, gw.StreamCacheKeys[i])
			tools = append(tools, gw.StreamTools[i])
		}
	}
	return calls, keys, tools
}

// roles returns the role of each of msgs.
func roles(msgs []message.Content) []message.Role {
	out := make([]message.Role, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

// titlings returns each titling stored in session sid, a chain of its own
// from a root of message.TypeTitling, in the order they were stored,
// failing when one branches.
func titlings(t *testing.T, a *App, sid message.SessionID) [][]message.Content {
	t.Helper()
	roots, err := a.Sessions().ForkChildren(t.Context(), sid, "", message.TypeTitling)
	if err != nil {
		t.Fatal(err)
	}
	out := make([][]message.Content, len(roots))
	for i, m := range roots {
		for {
			out[i] = append(out[i], m)
			kids, err := a.Sessions().ForkChildren(t.Context(), sid, m.ID, message.TypeTitling)
			if err != nil {
				t.Fatal(err)
			}
			if len(kids) == 0 {
				break
			}
			if len(kids) > 1 {
				t.Fatalf("%d titling messages below %s, want one", len(kids), m.ID)
			}
			m = kids[0]
		}
	}
	return out
}

// TestTitling_AsksAfreshEachTime checks that each prompt's titling sends a
// request of its own: the hook's system message and one user message of
// the prompts on the branch, with no tools, no earlier titling, and the
// session's cache key; that each is stored as a chain of its own, its
// system message, user message and reply, from a root linked to no
// message of the conversation; and that the session's branch leaves them
// out, while its tree reads them.
func TestTitling_AsksAfreshEachTime(t *testing.T) {
	a, gw := newActorTestApp(t, "Greeting the agent")
	a.extensions = append(a.extensions, &titler{name: "replying"})
	events := a.Subscribe(t.Context())

	const id = "ses_fresh"
	sendAndSettle(t, a, events, id, SendInput{Text: "hi", MessageID: "msg_first"})
	sendAndSettle(t, a, events, id, SendInput{Text: "again", MessageID: "msg_second"})

	calls, keys, tools := titleCalls(gw)
	if len(calls) != 2 {
		t.Fatalf("titling sent %d requests, want one for each prompt", len(calls))
	}
	tagged := Section(InstructionsTag, titleSystem)
	if got := texts(calls[0]); !slices.Equal(got, []string{tagged, "hi"}) {
		t.Errorf("first titling sent %q, want the system message and the prompt", got)
	}
	if got := texts(calls[1]); !slices.Equal(got, []string{tagged, "hi\n\nagain"}) {
		t.Errorf("second titling sent %q, want the system message and both prompts, and no earlier title", got)
	}
	for i := range calls {
		if keys[i] != id {
			t.Errorf("titling cache key = %q, want the session's", keys[i])
		}
		if len(tools[i]) != 0 {
			t.Errorf("titling offered tools %+v, want none", tools[i])
		}
		if set, ok := message.LastPart[message.ToolSetPart](calls[i][0].Content); !ok || len(set.Tools) != 0 {
			t.Errorf("titling sent system parts %+v, want an empty tool set among them", calls[i][0].Content)
		}
	}

	stored := titlings(t, a, id)
	if len(stored) != 2 {
		t.Fatalf("session holds %d titlings, want one for each prompt", len(stored))
	}
	var ids []message.ID
	for i, chain := range stored {
		if got, want := roles(chain), []message.Role{message.RoleSystem, message.RoleUser, message.RoleAgent}; !slices.Equal(got, want) {
			t.Fatalf("titling %d = %v, want %v", i, got, want)
		}
		if chain[0].ParentID != nil {
			t.Errorf("titling %d hangs below %s, want a root of its own", i, *chain[0].ParentID)
		}
		sent := make([]agent.Message, len(chain))
		for j, m := range chain {
			sent[j] = agent.Message{Role: m.Role, Content: m.Parts}
			ids = append(ids, m.ID)
		}
		if got, want := texts(sent), append(texts(calls[i]), "Greeting the agent"); !slices.Equal(got, want) {
			t.Errorf("titling %d stored %q, want %q", i, got, want)
		}
		if chain[2].Model == "" || chain[1].Model != chain[2].Model || chain[1].Provider != chain[2].Provider {
			t.Errorf("titling %d stored models %s/%q and %s/%q, want the prompt and reply of the model that wrote it", i, chain[1].Provider, chain[1].Model, chain[2].Provider, chain[2].Model)
		}
		if chain[0].Model != "" {
			t.Errorf("titling %d stored a system message of model %q, want none, as the conversation's", i, chain[0].Model)
		}
		set, ok := message.LastPart[message.ToolSetPart](chain[0].Parts)
		if len(chain[0].Parts) != 2 || !ok || set.Tools == nil || len(set.Tools) != 0 {
			t.Errorf("titling %d stored system parts %+v, want its text and an empty tool set", i, chain[0].Parts)
		}
	}
	for _, prompt := range []message.ID{"msg_first", "msg_second"} {
		if below, err := a.Sessions().ForkChildren(t.Context(), id, prompt, message.TypeTitling); err != nil || len(below) != 0 {
			t.Errorf("titling messages below %s = %d, %v; want none", prompt, len(below), err)
		}
	}

	whole, err := a.Sessions().Branch(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	if branch := conversation(t, whole); len(branch) != 4 {
		t.Errorf("branch = %+v; want the prompts and their replies alone", whole)
	}
	if kids, err := a.Sessions().Children(t.Context(), id, ""); err != nil || len(kids) != 1 {
		t.Errorf("conversation roots = %d, %v; want one, with no titling among them", len(kids), err)
	}
	tree, err := a.Session(id).Tree()
	if err != nil {
		t.Fatal(err)
	}
	treeForks := make([]message.ID, len(tree.Forks))
	for i, m := range tree.Forks {
		treeForks[i] = m.ID
	}
	if !slices.Equal(treeForks, ids) {
		t.Errorf("tree forks = %v, want the two titlings %v", treeForks, ids)
	}
}

// TestTitling_SkipsPromptsWithoutText checks that a branch whose prompts
// hold no text asks no hook and stores no titling.
func TestTitling_SkipsPromptsWithoutText(t *testing.T) {
	a, _ := newActorTestApp(t, "Greeting the agent")
	h := &titler{name: "replying"}
	a.extensions = append(a.extensions, h)
	events := a.Subscribe(t.Context())

	const id = "ses_blank"
	sendAndSettle(t, a, events, id, SendInput{Text: "  "})
	if n := len(h.asked()); n != 0 {
		t.Errorf("hook asked %d times, want none", n)
	}
	if stored := titlings(t, a, id); len(stored) != 0 {
		t.Errorf("session holds %d titlings, want none", len(stored))
	}
}
