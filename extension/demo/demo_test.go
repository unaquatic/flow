package demo

import (
	"context"
	"encoding/json"
	"go/format"
	"slices"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension/tools"
)

// TestModelUsage checks the usage the demo estimates for its requests:
// each sends the base and the text of its messages, reads from the cache, in
// whole blocks, the prompt that the request before it sent once that holds
// enough tokens, writes none to it, and counts its output.
func TestModelUsage(t *testing.T) {
	request := func(texts ...string) agent.Request {
		var req agent.Request
		for _, text := range texts {
			req.Messages = append(req.Messages, agent.NewUserMessage(text))
		}
		return req
	}
	prompt := strings.Repeat("p", 40)    // 10 tokens
	result := strings.Repeat("r", 4_000) // a call's result of 1,000 tokens
	var m Model
	steps := []struct {
		name   string
		req    agent.Request
		output string
		want   agent.Usage
	}{
		{name: "first", req: request(prompt), output: strings.Repeat("o", 400),
			want: agent.Usage{InputTokens: BaseTokens + 10, OutputTokens: 100}},
		// The first request sent too few tokens to cache.
		{name: "after a call", req: request(prompt, result),
			want: agent.Usage{InputTokens: BaseTokens + 1_010}},
		// 1,200 tokens sent before are read from the cache as 9 blocks.
		{name: "cached", req: request(prompt, result, "ok"), output: "ok",
			want: agent.Usage{InputTokens: BaseTokens + 1_011, CachedInputTokens: 1_152, OutputTokens: 1}},
		// A shorter history, as after an undo, reads at most what it sends.
		{name: "shorter", req: request(prompt),
			want: agent.Usage{InputTokens: BaseTokens + 10, CachedInputTokens: 128}},
	}
	for _, step := range steps {
		if got := m.Usage(EstimateRequest(step.req), step.output); got != step.want {
			t.Errorf("%s: usage = %+v, want %+v", step.name, got, step.want)
		}
	}
}

// TestModelStream checks that the demo's model answers each request with
// the next step, from the first again after the last, streamed as a
// provider's response: the step's text, its tool call with its whole input,
// the usage, and the stop of a reply that calls a tool or ends the turn.
func TestModelStream(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &Model{Steps: Segments[len(Segments)-2:]}
		for i, want := range []Segment{m.Steps[0], m.Steps[1], m.Steps[0]} {
			stream, err := m.Stream(t.Context(), agent.Request{})
			if err != nil {
				t.Fatal(err)
			}
			resp, err := stream.Collect()
			if err != nil {
				t.Fatal(err)
			}
			content := resp.Message.Content
			if text, ok := content[0].(message.TextPart); !ok || text.Text != want.Text {
				t.Errorf("reply %d starts with %+v, want the text %q", i, content[0], want.Text)
			}
			wantStop := agent.StopEndTurn
			if want.Tool != nil {
				wantStop = agent.StopToolUse
				call, ok := content[len(content)-1].(message.ToolUsePart)
				if !ok || call.Name != want.Tool.Name || string(call.Input) != string(want.Tool.Input) || m.Call != want.Tool {
					t.Errorf("reply %d calls %+v, want %s %s", i, content[len(content)-1], want.Tool.Name, want.Tool.Input)
				}
			} else if len(content) != 1 || m.Call != nil {
				t.Errorf("reply %d = %+v, want its text alone", i, content)
			}
			if resp.StopReason != wantStop || resp.Usage.InputTokens != BaseTokens || resp.Usage.OutputTokens == 0 {
				t.Errorf("reply %d stops with %q and usage %+v, want %q and an estimate", i, resp.StopReason, resp.Usage, wantStop)
			}
		}
	})
}

// TestModelStreamStopped checks that a stop ends the demo's stream with
// the context's error, as the stream of a cancelled request ends.
func TestModelStreamStopped(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		m := &Model{Steps: Segments}
		stream, err := m.Stream(ctx, agent.Request{})
		if err != nil {
			t.Fatal(err)
		}
		cancel()
		if _, err := stream.Collect(); err != context.Canceled {
			t.Errorf("stopped stream error = %v, want %v", err, context.Canceled)
		}
	})
}

// TestSegmentsCoverToolStates checks that the demo calls every built-in tool
// with valid input, and ends calls in every way a real call ends: completed,
// and failed with each reason a header shows.
func TestSegmentsCoverToolStates(t *testing.T) {
	called := map[string]bool{}
	var completed int
	var errs []string
	for _, seg := range Segments {
		if seg.Tool == nil {
			continue
		}
		if !json.Valid(seg.Tool.Input) {
			t.Errorf("%s input is not JSON: %s", seg.Tool.Name, seg.Tool.Input)
		}
		called[seg.Tool.Name] = true
		switch st := seg.Tool.State(time.Time{}, time.Time{}).(type) {
		case message.ToolStateCompleted:
			completed++
		case message.ToolStateError:
			errs = append(errs, st.Error)
		}
	}
	for _, builtin := range tools.All() {
		if !called[builtin.Name] {
			t.Errorf("the demo never calls %s", builtin.Name)
		}
	}
	if completed == 0 {
		t.Error("no demo call completes")
	}
	ends := map[string]func(string) bool{
		"exit with output":    func(e string) bool { return strings.HasSuffix(e, "\n\n[exit 1]") },
		"exit without output": func(e string) bool { return e == "[exit 1]" },
		"timeout":             func(e string) bool { return strings.HasSuffix(e, "\n\n[timeout after 120s]") },
		"cancelled":           func(e string) bool { return strings.HasSuffix(e, "\n\n[cancelled]") },
		"tool not found":      func(e string) bool { return strings.HasPrefix(e, agent.ErrToolNotFound.Error()+": ") },
		"tool failure":        func(e string) bool { return strings.HasSuffix(e, "\n\n[tool execution failed]") },
		"interrupted":         func(e string) bool { return e == message.InterruptedError },
	}
	for name, matches := range ends {
		if !slices.ContainsFunc(errs, matches) {
			t.Errorf("no demo call fails with %s", name)
		}
	}
}

// TestTools checks that the demo has a tool for each tool its calls name,
// every built-in tool among them, but for webfetch, which it leaves out, so
// that its call fails as not found, as the call's segment says it does.
func TestTools(t *testing.T) {
	names := ToolNames()
	for _, builtin := range tools.All() {
		if !slices.Contains(names, builtin.Name) {
			t.Errorf("the demo has no %s tool", builtin.Name)
		}
	}
	svc := Tools(&Model{})
	if _, ok := svc.Get("webfetch"); ok || slices.Contains(names, "webfetch") {
		t.Error("the demo has a webfetch tool, want its call to fail as not found")
	}
	if res := svc.Execute(t.Context(), agent.ToolCall{ID: "c1", Name: "webfetch"}); !res.IsError || !strings.HasPrefix(res.Content, agent.ErrToolNotFound.Error()) {
		t.Errorf("webfetch call = %+v, want it not found", res)
	}
}

// TestTodoOutput checks that each todo call of the demo returns the list as
// the built-in todo tool returns it for the call's input.
func TestTodoOutput(t *testing.T) {
	i := slices.IndexFunc(tools.All(), func(tl agent.Tool) bool { return tl.Name == "todo" })
	if i < 0 {
		t.Fatal("no built-in todo tool")
	}
	todo := tools.All()[i]
	for _, seg := range Segments {
		if seg.Tool == nil || seg.Tool.Name != "todo" {
			continue
		}
		var args map[string]any
		if err := json.Unmarshal(seg.Tool.Input, &args); err != nil {
			t.Fatal(err)
		}
		out, err := todo.Execute(t.Context(), args)
		if err != nil {
			t.Fatal(err)
		}
		if out.Text != seg.Tool.Output {
			t.Errorf("todo %s returns %q, want the demo's output %q", seg.Tool.Input, out.Text, seg.Tool.Output)
		}
	}
}

// TestDemoSourcesAreFormatted checks that the Go files the demo writes and
// shows read as gofmt writes them.
func TestDemoSourcesAreFormatted(t *testing.T) {
	for name, src := range map[string]string{
		"divide_cases_test.go": DivideCasesSource(),
		"calc.go":              finalSource,
	} {
		formatted, err := format.Source([]byte(src))
		if err != nil {
			t.Errorf("%s does not parse: %v", name, err)
			continue
		}
		if string(formatted) != src {
			t.Errorf("%s is not formatted; gofmt writes:\n%s", name, formatted)
		}
	}
}

// TestDivideCasesRun checks that the run of the demo's table-driven test
// runs and passes each of DivideCases, by the name go test gives it.
func TestDivideCasesRun(t *testing.T) {
	run := DivideCasesRun()
	for _, c := range DivideCases {
		name := "TestDivideCases/" + strings.ReplaceAll(c.Name, " ", "_")
		if !strings.Contains(run, "=== RUN   "+name+"\n") || !strings.Contains(run, "--- PASS: "+name+" ") {
			t.Errorf("the run does not run and pass %s:\n%s", name, run)
		}
	}
}
