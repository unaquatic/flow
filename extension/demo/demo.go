// Package demo is the extension that plays a canned session, as the /demo
// command of its ui package starts it, and that the terminal's golden frames
// and the landing page draw: a model that streams Segments as a provider
// streams replies, the tools that answer its calls, and the prompts that
// open its turns, which Script puts together as an app.Script. It plugs into
// flow at the terminal UI alone, and adds nothing to the app's runs.
package demo

import (
	"context"
	"errors"
	"iter"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
)

// Script returns the demo as a run app.Session.Play plays: turns that each
// open with the next of Prompts, waiting TurnPause between them, as Next
// gives them, which a Model streaming Segments answers, every step a turn,
// with the calls of its replies run by Tools.
func Script() app.Script {
	model := &Model{Steps: Segments}
	return app.Script{
		Provider: model,
		Variant:  agent.Variant{Provider: Provider, Model: ModelName},
		Tools:    Tools(model),
		MaxSteps: len(model.Steps),
		Next:     Next,
	}
}

// Next returns the prompt that opens turn i of the demo, the next of
// Prompts, after waiting TurnPause for every turn but the first. It reports
// false when ctx ends while it waits.
func Next(ctx context.Context, i int) (string, bool) {
	if i > 0 && Sleep(ctx, TurnPause) {
		return "", false
	}
	return Prompts[i%len(Prompts)], true
}

// Provider and ModelName are the model the demo stands in for, which its
// messages carry.
const (
	Provider  agent.ProviderID = "openai"
	ModelName                  = "openai/gpt-6-luna"
)

// The pace of the demo's replies, as a provider streams them and its tools
// run.
const (
	// firstTokenDelay is the time from a request to the first part of
	// its reply.
	firstTokenDelay = 40 * time.Millisecond
	// wordDelay is the time between the words of a reply's text.
	wordDelay = 3 * time.Millisecond
	// chunkPause is the time between the chunks of a call's input.
	chunkPause = 20 * time.Millisecond
	// toolPause is the time a call runs.
	toolPause = 300 * time.Millisecond
	// TurnPause is the time between the end of a turn and the next
	// prompt.
	TurnPause = 2 * time.Millisecond
)

// The demo estimates the token use of its requests as ModelName counts that
// of real ones.
const (
	// BaseTokens is what every request sends before the conversation:
	// the instructions and the tool definitions, about what the first
	// request of a real session counts.
	BaseTokens = 190
	// bytesPerToken is the bytes of text a token holds on average.
	bytesPerToken = 4
	// cacheBlock is the size of the blocks in which the prefix of a
	// prompt is read from the cache, and cacheMin the least prompt whose
	// prefix the cache holds.
	cacheBlock = 128
	cacheMin   = 1024
)

// errNoCall reports a demo tool run while the model's last reply called
// none.
var errNoCall = errors.New("no demo call to answer")

// Model stands in for a provider and its model in the demo. It answers
// each request with the next of Steps, going from the last back to the
// first, streamed as a provider streams a response: after
// firstTokenDelay, its text a word at a time, then its tool call, its
// input in chunks, and last the usage that Usage estimates for the request.
// It sends no requests, and takes any model ID for its own. It is not safe
// for concurrent use, as the model of an agent.Agent need not be.
type Model struct {
	// Steps are the replies it streams.
	Steps []Segment
	// Next is the index in Steps of the reply to the next request.
	Next int
	// Call is the tool call of the last reply, which Tools answers, or
	// nil when that reply calls none.
	Call *Tool
	// Sent is the prompt tokens of the last request, which the next request
	// reads from the cache.
	Sent int
}

// Name returns the ID of the provider the demo stands in for.
func (m *Model) Name() string { return string(Provider) }

// Model returns m, whatever model ID it is asked for.
func (m *Model) Model(string) agent.Model { return m }

// Stream answers req with the next of Steps. A stop ends the stream with
// ctx's error, as the stream of a cancelled request ends.
func (m *Model) Stream(ctx context.Context, req agent.Request) (agent.StreamResponse, error) {
	step := m.Steps[m.Next%len(m.Steps)]
	m.Next++
	m.Call = step.Tool
	output := []string{step.Text}
	if step.Tool != nil {
		output = append(output, string(step.Tool.Input))
	}
	usage := m.Usage(EstimateRequest(req), output...)
	return func(yield func(agent.StreamPart, error) bool) {
		// pause waits d, and reports whether the stream goes on.
		pause := func(d time.Duration) bool {
			if Sleep(ctx, d) {
				yield(nil, ctx.Err())
				return false
			}
			return true
		}
		if !pause(firstTokenDelay) {
			return
		}
		for i := 0; i < len(step.Text); {
			j := wordEnd(step.Text, i)
			if !yield(agent.TextDelta{Text: step.Text[i:j]}, nil) || !pause(wordDelay) {
				return
			}
			i = j
		}
		reason := agent.StopEndTurn
		if call := step.Tool; call != nil {
			id := "call_" + string(message.NewPartID())
			if !yield(agent.ToolUseStart{ID: id, Name: call.Name}, nil) {
				return
			}
			for chunk := range chunks(string(call.Input)) {
				if !yield(agent.ToolUseDelta{ID: id, JSON: chunk}, nil) || !pause(chunkPause) {
					return
				}
			}
			if !yield(agent.ToolUseEnd{ID: id, Input: call.Input}, nil) {
				return
			}
			reason = agent.StopToolUse
		}
		if yield(usage, nil) {
			yield(agent.Stop{Reason: reason}, nil)
		}
	}, nil
}

// Usage returns the estimated usage of a request that sends prompt tokens
// and replies with output, and records the request as sent. The prompt that
// the request before it sent is read from the cache, in whole cacheBlock
// blocks once it holds cacheMin tokens, and, as ModelName reports, none
// is written to it.
func (m *Model) Usage(prompt int, output ...string) agent.Usage {
	cached := 0
	if m.Sent >= cacheMin {
		cached = min(m.Sent, prompt) / cacheBlock * cacheBlock
	}
	m.Sent = prompt
	out := 0
	for _, text := range output {
		out += EstimateTokens(text)
	}
	return agent.Usage{InputTokens: prompt, CachedInputTokens: cached, OutputTokens: out}
}

// EstimateRequest returns the estimated prompt tokens of req: BaseTokens
// for its instructions and tool definitions, and the text of its messages,
// their tool calls, and the results of the calls, as EstimateTokens counts
// it.
func EstimateRequest(req agent.Request) int {
	n := BaseTokens
	for _, msg := range req.Messages {
		for _, part := range msg.Content {
			switch p := part.(type) {
			case message.TextPart:
				n += EstimateTokens(p.Text)
			case message.ReasoningPart:
				n += EstimateTokens(p.Text)
			case message.ToolUsePart:
				n += EstimateTokens(p.Name) + EstimateTokens(string(p.Input))
			case message.ToolResultPart:
				n += EstimateTokens(p.Text)
			}
		}
	}
	return n
}

// EstimateTokens returns the estimated tokens of text, at bytesPerToken
// bytes each.
func EstimateTokens(text string) int {
	return (len(text) + bytesPerToken - 1) / bytesPerToken
}

// Tools returns the tools of the demo: one for each tool its Segments call,
// as ToolNames lists them, each of which answers the call that model
// streamed last. A call runs for toolPause and returns what that call's Run
// gives. A call that ctx stops first fails as interrupted. A call of a tool
// the demo leaves out fails as not found, as a real one does. The tools have
// no description or schema, which no provider reads: the demo's model sends
// no requests.
func Tools(model *Model) *agent.Tools {
	tools := agent.NewTools()
	for _, name := range ToolNames() {
		tools.Register(agent.Tool{Name: name, Execute: func(ctx context.Context, _ map[string]any) (agent.ToolOutput, error) {
			call := model.Call
			if call == nil {
				return agent.ToolOutput{}, errNoCall
			}
			if Sleep(ctx, toolPause) {
				return agent.ToolOutput{Text: message.InterruptedError, IsError: true}, nil
			}
			return call.Run(), nil
		}})
	}
	return tools
}

// ToolNames returns the names of the tools that Segments call, in the order
// they first call them, but for those the demo leaves out, as Missing tells.
func ToolNames() []string {
	var names []string
	for _, seg := range Segments {
		if seg.Tool != nil && !seg.Tool.Missing() && !slices.Contains(names, seg.Tool.Name) {
			names = append(names, seg.Tool.Name)
		}
	}
	return names
}

// Missing reports whether the call's tool is one the demo leaves out, so
// that the call fails as not found: whether its Error is the failure of a
// call of a tool that is not found.
func (t Tool) Missing() bool {
	return strings.HasPrefix(t.Error, agent.ErrToolNotFound.Error()+": ")
}

// Run returns what the call's tool returns when it ends: Output, or, when
// Error is set, Error as the text of a failed run, which holds the marker
// of the failure, as agent.FailedToolRun and agent.ToolFailure end it.
func (t Tool) Run() agent.ToolOutput {
	if t.Error != "" {
		return agent.ToolOutput{Text: t.Error, IsError: true}
	}
	return agent.ToolOutput{Text: t.Output}
}

// State returns the last state of the call once it ran from started to
// ended: failed with Error when Error is set, and completed with Output
// otherwise.
func (t Tool) State(started, ended time.Time) message.ToolState {
	if t.Error != "" {
		return message.ToolStateError{Input: t.Input, Error: t.Error, StartedAt: started, EndedAt: ended}
	}
	return message.ToolStateCompleted{Input: t.Input, Output: t.Output, StartedAt: started, EndedAt: ended}
}

// chunks yields small chunks without splitting UTF-8 characters.
func chunks(text string) iter.Seq[string] {
	return func(yield func(string) bool) {
		for start := 0; start < len(text); {
			end := min(start+16, len(text))
			for end < len(text) && !utf8.RuneStart(text[end]) {
				end++
			}
			if !yield(text[start:end]) {
				return
			}
			start = end
		}
	}
}

// wordEnd returns the end of the next word and its trailing whitespace.
func wordEnd(s string, i int) int {
	j := i
	for j < len(s) && s[j] != ' ' && s[j] != '\t' && s[j] != '\n' {
		j++
	}
	for j < len(s) && (s[j] == ' ' || s[j] == '\t' || s[j] == '\n') {
		j++
	}
	return max(j, i+1)
}

// Sleep pauses for d and reports whether ctx ended first.
func Sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-t.C:
		return false
	}
}
