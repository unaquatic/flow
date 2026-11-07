package agent_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/agenttest"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/database"
	"github.com/unaquatic/flow/extension"
	"github.com/unaquatic/flow/extension/snapshot"
	_ "github.com/unaquatic/sqlite" // registers the driver that database.Open uses
)

// loadSnapshot loads the snapshot extension over worktree, with its tables
// in a database in a temporary directory, as the app loads it.
func loadSnapshot(t *testing.T, worktree string) *snapshot.Extension {
	t.Helper()
	db, err := database.Open(filepath.Join(t.TempDir(), database.FileName))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	project, err := database.Project(t.Context(), db, worktree)
	if err != nil {
		t.Fatal(err)
	}
	e, err := snapshot.Load(t.Context(), extension.Host{
		DB:         db,
		Project:    project,
		WorkingDir: worktree,
		DataDir:    t.TempDir(),
		Config:     &config.Config{},
	})
	if err != nil {
		t.Fatal(err)
	}
	return e.(*snapshot.Extension)
}

// snapshotForAgentTest returns the snapshot extension over an isolated
// worktree with one file, whose step parts start pending at once.
func snapshotForAgentTest(t *testing.T) *snapshot.Extension {
	t.Helper()
	worktree := filepath.Join(t.TempDir(), "worktree")
	if err := os.MkdirAll(worktree, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktree, "seed.txt"), []byte("seed"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := loadSnapshot(t, worktree)
	s.IndexingDelay = 0
	return s
}

// newDisabledSnapshot returns a snapshot extension that records nothing.
func newDisabledSnapshot(t *testing.T) *snapshot.Extension {
	t.Helper()
	s := loadSnapshot(t, t.TempDir())
	s.Service.Disabled = true
	return s
}

// partKind tells the part events apart.
type partKind string

const (
	partStarted partKind = "started"
	partUpdated partKind = "updated"
	partEnded   partKind = "ended"
)

// partChangesOf returns the kind and change of each part event in events, in
// emission order.
func partChangesOf(events []agent.Event) (kinds []partKind, changes []agent.PartChange) {
	for _, ev := range events {
		switch e := ev.(type) {
		case agent.PartStarted:
			kinds, changes = append(kinds, partStarted), append(changes, e.PartChange)
		case agent.PartUpdated:
			kinds, changes = append(kinds, partUpdated), append(changes, e.PartChange)
		case agent.PartEnded:
			kinds, changes = append(kinds, partEnded), append(changes, e.PartChange)
		}
	}
	return kinds, changes
}

// TestAgentEvents_PartsRebuildTheFinalMessage checks that applying a
// message's part events in order gives its final parts, its step part
// included, and that every part ends exactly once.
func TestAgentEvents_PartsRebuildTheFinalMessage(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ReasoningDelta{Text: "let me "},
				agent.ReasoningDelta{Text: "think"},
				agent.TextDelta{Text: "hello "},
				agent.TextDelta{Text: "world"},
				agent.Usage{InputTokens: 12, OutputTokens: 5},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		TurnHooks: []agent.TurnHook{snapshotForAgentTest(t)},
	}

	turns, events, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if len(turns) != 1 {
		t.Fatalf("expected 1 turn, got %d", len(turns))
	}
	if !slices.ContainsFunc(turns[0].Parts, func(p message.ContentPart) bool { _, ok := p.(message.UsagePart); return ok }) {
		t.Fatalf("final parts = %+v, want a usage part among them", turns[0].Parts)
	}
	var parts []message.ContentPart
	ended := make(map[message.PartID]int)
	types, changes := partChangesOf(events)
	for i, c := range changes {
		j := slices.IndexFunc(parts, func(p message.ContentPart) bool { return p.PartID() == c.Part.PartID() })
		switch {
		case types[i] == partStarted && j < 0:
			parts = append(parts, c.Part)
		case types[i] != partStarted && j >= 0:
			parts[j] = c.Part
		default:
			t.Fatalf("%s for part %q, which has started = %v", types[i], c.Part.PartID(), j >= 0)
		}
		if types[i] == partEnded {
			ended[c.Part.PartID()]++
		}
	}
	if !reflect.DeepEqual(parts, turns[0].Parts) {
		t.Fatalf("rebuilt parts = %+v, want the final parts %+v", parts, turns[0].Parts)
	}
	for _, p := range parts {
		if ended[p.PartID()] != 1 {
			t.Errorf("part %T %q ended %d times, want once", p, p.PartID(), ended[p.PartID()])
		}
	}
}

// TestAgentEvents_PartEventsCarryTextSoFar checks that a block's first delta
// starts its part, each later delta updates it with the text so far, and the
// end of the stream ends it, under one part ID per block.
func TestAgentEvents_PartEventsCarryTextSoFar(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ReasoningDelta{Block: 0, Text: "let me "},
				agent.ReasoningDelta{Block: 0, Text: "think"},
				agent.TextDelta{Block: 1, Text: "hello "},
				agent.TextDelta{Block: 1, Text: "world"},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	_, events, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}

	// block says which of the two blocks each event belongs to.
	want := []struct {
		typ   partKind
		text  string
		block int
	}{
		{partStarted, "let me ", 0},
		{partUpdated, "let me think", 0},
		{partStarted, "hello ", 1},
		{partUpdated, "hello world", 1},
		{partEnded, "let me think", 0},
		{partEnded, "hello world", 1},
	}
	types, changes := partChangesOf(events)
	if len(changes) != len(want) {
		t.Fatalf("got part events %v, want %d", types, len(want))
	}
	ids := make(map[int]message.PartID)
	for i, w := range want {
		var text string
		switch p := changes[i].Part.(type) {
		case message.ReasoningPart:
			text = p.Text
		case message.TextPart:
			text = p.Text
		}
		if types[i] != w.typ || text != w.text {
			t.Errorf("part event %d = %s %q, want %s %q", i, types[i], text, w.typ, w.text)
		}
		id := changes[i].Part.PartID()
		if first, ok := ids[w.block]; ok && first != id {
			t.Errorf("part event %d has part ID %q, want block %d's %q", i, id, w.block, first)
		}
		ids[w.block] = id
	}
	if ids[0] == ids[1] {
		t.Errorf("both blocks have part ID %q, want one each", ids[0])
	}
}

// TestAgentEvents_ToolInputUpdatesKeepEarlierInput checks that each part
// event of a pending call carries the tool input streamed so far, that
// later chunks leave the input of earlier events as it was, and that an end
// with the input the chunks built updates nothing.
func TestAgentEvents_ToolInputUpdatesKeepEarlierInput(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ToolUseStart{ID: "call_1", Name: "echo"},
				agent.ToolUseDelta{ID: "call_1", JSON: `{"text":`},
				agent.ToolUseDelta{ID: "call_1", JSON: `"hi"}`},
				agent.ToolUseEnd{ID: "call_1", Input: json.RawMessage(`{"text":"hi"}`)},
				agent.Stop{Reason: agent.StopToolUse},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
		MaxSteps: 1,
	}

	_, events, _ := runAgent(t, context.Background(), ag)
	var inputs []string
	_, changes := partChangesOf(events)
	for _, c := range changes {
		if tp, ok := c.Part.(message.ToolPart); ok {
			if _, pending := tp.State.(message.ToolStatePending); pending {
				inputs = append(inputs, string(tp.Input()))
			}
		}
	}
	if want := []string{"", `{"text":`, `{"text":"hi"}`}; !slices.Equal(inputs, want) {
		t.Errorf("pending inputs in part events = %q, want %q", inputs, want)
	}
}

// TestAgentEvents_InterleavedBlocksStaySeparate checks block order and identity.
func TestAgentEvents_InterleavedBlocksStaySeparate(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ReasoningDelta{Block: 0, Text: "first thought"},
				agent.TextDelta{Block: 1, Text: "first answer"},
				agent.ReasoningDelta{Block: 2, Text: "second thought"},
				agent.TextDelta{Block: 3, Text: "second answer"},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}

	want := []struct {
		kind string
		text string
	}{
		{"reasoning", "first thought"},
		{"text", "first answer"},
		{"reasoning", "second thought"},
		{"text", "second answer"},
	}
	parts := turns[0].Parts
	if len(parts) != len(want) {
		t.Fatalf("expected %d parts, got %d: %+v", len(want), len(parts), parts)
	}
	for i, w := range want {
		switch p := parts[i].(type) {
		case message.ReasoningPart:
			if w.kind != "reasoning" || p.Text != w.text {
				t.Fatalf("parts[%d] = ReasoningPart(%q), want %s(%q)", i, p.Text, w.kind, w.text)
			}
		case message.TextPart:
			if w.kind != "text" || p.Text != w.text {
				t.Fatalf("parts[%d] = TextPart(%q), want %s(%q)", i, p.Text, w.kind, w.text)
			}
		default:
			t.Fatalf("parts[%d] = %T, want %s", i, parts[i], w.kind)
		}
	}
}

// TestAgentEvents_SameBlockKeepsOneRun checks that one block produces one part.
func TestAgentEvents_SameBlockKeepsOneRun(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.TextDelta{Block: 0, Text: "hello "},
				agent.TextDelta{Block: 0, Text: "there "},
				agent.TextDelta{Block: 0, Text: "world"},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if len(turns[0].Parts) != 1 {
		t.Fatalf("expected 1 merged part, got %+v", turns[0].Parts)
	}
	tp, ok := turns[0].Parts[0].(message.TextPart)
	if !ok || tp.Text != "hello there world" {
		t.Fatalf("parts[0] = %+v, want TextPart('hello there world')", turns[0].Parts[0])
	}
}

// TestAgentEvents_ReasoningBecomesAPart checks reasoning part creation.
func TestAgentEvents_ReasoningBecomesAPart(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ReasoningDelta{Text: "thinking..."},
				agent.TextDelta{Text: "answer"},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if len(turns[0].Parts) != 2 {
		t.Fatalf("expected reasoning + text, got %+v", turns[0].Parts)
	}
	rp, ok := turns[0].Parts[0].(message.ReasoningPart)
	if !ok || rp.Text != "thinking..." {
		t.Fatalf("parts[0] = %+v, want ReasoningPart 'thinking...'", turns[0].Parts[0])
	}
	tp, ok := turns[0].Parts[1].(message.TextPart)
	if !ok || tp.Text != "answer" {
		t.Fatalf("parts[1] = %+v, want TextPart 'answer'", turns[0].Parts[1])
	}
}

// TestAgentEvents_ReasoningKeepsItsSignature checks that a reasoning block's
// signature reaches its part, and the history the next request sends, also
// for a block that streamed no text, and that a redacted block stays marked.
func TestAgentEvents_ReasoningKeepsItsSignature(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
			return agenttest.Stream(
				agent.ReasoningDelta{Block: 0, Text: "think"},
				agent.ReasoningSignature{Block: 0, Signature: "sig-0"},
				agent.TextDelta{Block: 1, Text: "answer"},
				agent.ReasoningSignature{Block: 2, Signature: "sig-2"},
				agent.ReasoningSignature{Block: 3, Signature: "enc-3", Redacted: true},
				agent.Stop{Reason: agent.StopEndTurn},
			), nil
		}}, "test"),
		Tools:    newTools(),
		Variant:  agent.Variant{Provider: "test", Model: "test-model"},
		Messages: []agent.Message{agent.NewUserMessage("hi")},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	type reasoning struct {
		Text, Signature string
		Redacted        bool
	}
	want := []reasoning{{"think", "sig-0", false}, {"", "sig-2", false}, {"", "enc-3", true}}
	for name, parts := range map[string][]message.ContentPart{
		"final message": turns[0].Parts,
		"history":       ag.Messages[1].Content,
	} {
		var got []reasoning
		for _, p := range parts {
			if rp, ok := p.(message.ReasoningPart); ok {
				got = append(got, reasoning{rp.Text, rp.Signature, rp.Redacted})
			}
		}
		if !slices.Equal(got, want) {
			t.Errorf("%s reasoning = %+v, want %+v", name, got, want)
		}
	}
}

// TestAgentEvents_MessageLifecycle checks that a message's events open with
// MessageStarted, start and end its step part after all its content, and
// close with MessageEnded.
func TestAgentEvents_MessageLifecycle(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse("hi"), nil }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		TurnHooks: []agent.TurnHook{snapshotForAgentTest(t)},
	}

	turns, events, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if len(events) < 3 {
		t.Fatalf("got %d events, want a start, part events and an end", len(events))
	}
	if started, ok := events[0].(agent.MessageStarted); !ok || started.Message.ID != turns[0].ID {
		t.Errorf("first event = %+v, want MessageStarted for %q", events[0], turns[0].ID)
	}
	if ended, ok := events[len(events)-1].(agent.MessageEnded); !ok || ended.Message.ID != turns[0].ID {
		t.Errorf("last event = %+v, want MessageEnded for %q", events[len(events)-1], turns[0].ID)
	}
	types, changes := partChangesOf(events)
	n := len(changes)
	if n < 3 {
		t.Fatalf("got part events %v, want content, then the step part's start and end", types)
	}
	if _, ok := changes[n-2].Part.(snapshot.StepPart); !ok || types[n-2] != partStarted {
		t.Errorf("second-last part event = %s %T, want %s StepPart", types[n-2], changes[n-2].Part, partStarted)
	}
	if types[n-1] != partEnded || changes[n-1].Part.PartID() != changes[n-2].Part.PartID() {
		t.Errorf("last part event = %s %q, want %s of the step part", types[n-1], changes[n-1].Part.PartID(), partEnded)
	}
	parts := turns[0].Parts
	if step, ok := parts[len(parts)-1].(snapshot.StepPart); !ok || step.Snapshot.IsZero() {
		t.Errorf("last part = %+v, want a step part with a tree", parts[len(parts)-1])
	}
	if steps := countSteps(parts); steps != 1 {
		t.Errorf("message holds %d step parts, want 1", steps)
	}
}

// TestAgentEvents_StepPendingWhileSlow checks that a turn's step part starts
// pending when recording the workspace takes longer than the snapshot
// extension's IndexingDelay, as a delay of zero makes it, so that the reply
// shows the snapshot being taken, and starts with its tree when recording is
// quicker. Either way it ends with the tree, under the ID it started with.
func TestAgentEvents_StepPendingWhileSlow(t *testing.T) {
	for _, tt := range []struct {
		name    string
		delay   time.Duration
		pending bool
	}{
		{name: "slow", delay: 0, pending: true},
		{name: "quick", delay: time.Hour, pending: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			snaps := snapshotForAgentTest(t)
			snaps.IndexingDelay = tt.delay
			ag := &agent.Agent{
				Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse("hi"), nil }}, "test"),
				Tools:     newTools(),
				Variant:   agent.Variant{Provider: "test", Model: "test-model"},
				Messages:  []agent.Message{agent.NewUserMessage("hi")},
				TurnHooks: []agent.TurnHook{snaps},
			}
			_, events, err := runAgent(t, context.Background(), ag)
			if err != nil {
				t.Fatalf("agent run: %v", err)
			}
			var steps []snapshot.StepPart
			_, changes := partChangesOf(events)
			for _, c := range changes {
				if step, ok := c.Part.(snapshot.StepPart); ok {
					steps = append(steps, step)
				}
			}
			if len(steps) != 2 {
				t.Fatalf("got %d step part events, want its start and end", len(steps))
			}
			started, ended := steps[0], steps[1]
			if started.Pending != tt.pending || started.Pending == !started.Snapshot.IsZero() {
				t.Errorf("step started as %+v, want pending = %v, with a tree unless pending", started, tt.pending)
			}
			if ended.Pending || ended.Snapshot.IsZero() || ended.ID != started.ID {
				t.Errorf("step ended as %+v, want the part %q with its tree", ended, started.ID)
			}
		})
	}
}

// countSteps returns the number of step parts among parts.
func countSteps(parts []message.ContentPart) int {
	n := 0
	for _, p := range parts {
		if _, ok := p.(snapshot.StepPart); ok {
			n++
		}
	}
	return n
}

// TestAgentEvents_UsageBecomesOnePart checks that the usage a provider
// reports becomes one usage part once the response stops streaming, after
// the tool calls the response made and before its step part, holding the
// counts of the last report, which replace those before it. A response that
// reports no usage has no usage part.
func TestAgentEvents_UsageBecomesOnePart(t *testing.T) {
	last := agent.Usage{InputTokens: 9, CachedInputTokens: 4, OutputTokens: 3}
	tests := []struct {
		name    string
		reports []agent.StreamPart
		want    []message.Usage
	}{
		{name: "reported twice", reports: []agent.StreamPart{agent.Usage{InputTokens: 9}, last},
			want: []message.Usage{agent.UsageFrom(last)}},
		{name: "not reported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := &agent.Agent{
				Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
					return agenttest.Stream(slices.Concat(
						[]agent.StreamPart{
							agent.TextDelta{Text: "Listing."},
							agent.ToolUseStart{ID: "call_1", Name: "echo"},
							agent.ToolUseEnd{ID: "call_1", Input: json.RawMessage(`{"text":"hi"}`)},
						},
						tt.reports,
						[]agent.StreamPart{agent.Stop{Reason: agent.StopToolUse}},
					)...), nil
				}}, "test"),
				Tools: newTools(agent.Tool{
					Name: "echo",
					Execute: func(context.Context, map[string]any) (agent.ToolOutput, error) {
						return agent.ToolOutput{Text: "hi"}, nil
					},
				}),
				Variant:   agent.Variant{Provider: "test", Model: "test-model"},
				Messages:  []agent.Message{agent.NewUserMessage("hi")},
				TurnHooks: []agent.TurnHook{snapshotForAgentTest(t)},
				MaxSteps:  1,
			}

			turns, events, _ := runAgent(t, context.Background(), ag)
			if len(turns) != 1 {
				t.Fatalf("got %d turns, want 1", len(turns))
			}
			parts := turns[0].Parts
			var got []message.Usage
			for i, p := range parts {
				u, ok := p.(message.UsagePart)
				if !ok {
					continue
				}
				got = append(got, u.Usage)
				if _, ok := parts[i-1].(message.ToolPart); !ok {
					t.Errorf("usage follows %T, want the tool call", parts[i-1])
				}
				if _, ok := parts[i+1].(snapshot.StepPart); !ok {
					t.Errorf("usage precedes %T, want the step part", parts[i+1])
				}
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("usage parts = %+v, want %+v", got, tt.want)
			}
			if u := turns[0].Usage(); len(tt.want) > 0 && u != tt.want[0] {
				t.Errorf("ended message usage = %+v, want %+v", u, tt.want[0])
			}

			types, changes := partChangesOf(events)
			var usageEvents []partKind
			for i, c := range changes {
				if _, ok := c.Part.(message.UsagePart); ok {
					usageEvents = append(usageEvents, types[i])
				}
			}
			if want := len(tt.want); want > 0 && !slices.Equal(usageEvents, []partKind{partStarted, partEnded}) ||
				want == 0 && len(usageEvents) > 0 {
				t.Errorf("usage part events = %v, want one start and one end per usage part", usageEvents)
			}
		})
	}
}

// TestAgentEvents_Retrying checks that each request that fails with a
// transient error, but the last, is reported as Retrying for the message in
// progress, after its start and before its first part, and that the agent
// waits the policy's delay after each before it sends the request again. A
// last attempt that fails ends the message with its error instead.
func TestAgentEvents_Retrying(t *testing.T) {
	limited := &agent.ResponseError{Provider: "test", StatusCode: 429, Message: "slow down"}
	for _, tt := range []struct {
		name     string
		failures int
		retries  int
		ok       bool
	}{
		{name: "recovers", failures: 2, retries: 2, ok: true},
		{name: "gives up", failures: 3, retries: 2},
	} {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				calls := 0
				ag := &agent.Agent{
					Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) {
						if calls++; calls <= tt.failures {
							return nil, limited
						}
						return agenttest.TextResponse("hi"), nil
					}}, "test"),
					Tools:       newTools(),
					Variant:     agent.Variant{Provider: "test", Model: "test-model"},
					Messages:    []agent.Message{agent.NewUserMessage("hi")},
					RetryPolicy: agent.Policy{MaxAttempts: 3, Delay: 5 * time.Second},
				}
				start := time.Now()
				_, events, err := runAgent(t, t.Context(), ag)
				if (err == nil) != tt.ok {
					t.Fatalf("agent run = %v, want success %v", err, tt.ok)
				}
				if waited, want := time.Since(start), time.Duration(tt.retries)*5*time.Second; waited != want {
					t.Errorf("agent waited %v, want %v", waited, want)
				}

				started, ok := events[0].(agent.MessageStarted)
				if !ok {
					t.Fatalf("first event = %T, want MessageStarted", events[0])
				}
				var want []agent.Event
				for i := range tt.retries {
					want = append(want, agent.Retrying{MessageID: started.Message.ID, Attempt: i + 1, MaxAttempts: 3,
						Delay: 5 * time.Second, Reason: "rate limited", Err: limited})
				}
				if got := events[1 : 1+tt.retries]; !reflect.DeepEqual(got, want) {
					t.Errorf("events after the start = %+v, want %+v", got, want)
				}
				if n := len(eventsOfType[agent.Retrying](events)); n != tt.retries {
					t.Errorf("got %d Retrying events, want %d", n, tt.retries)
				}
				ended := eventsOfType[agent.MessageEnded](events)
				if len(ended) != 1 || (ended[0].Err == nil) != tt.ok {
					t.Errorf("MessageEnded = %+v, want one, failed unless the request recovered", ended)
				}
			})
		})
	}
}

// TestAgentEvents_NoStepMarkersWithoutSnapshot checks that nil snapshots emit no
// snapshot metadata.
func TestAgentEvents_NoStepMarkersWithoutSnapshot(t *testing.T) {
	ag := &agent.Agent{
		Providers: agenttest.NewProviders(t, &agenttest.Gateway{StreamFunc: func() (agent.StreamResponse, error) { return agenttest.TextResponse("hi"), nil }}, "test"),
		Tools:     newTools(),
		Variant:   agent.Variant{Provider: "test", Model: "test-model"},
		Messages:  []agent.Message{agent.NewUserMessage("hi")},
		TurnHooks: []agent.TurnHook{newDisabledSnapshot(t)},
	}

	turns, _, err := runAgent(t, context.Background(), ag)
	if err != nil {
		t.Fatalf("agent run: %v", err)
	}
	if n := countSteps(turns[0].Parts); n != 0 {
		t.Fatalf("message holds %d step parts, want none with snapshots disabled", n)
	}
}
