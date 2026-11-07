package demo

import (
	"context"
	"slices"
	"testing"
	"testing/synctest"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/internal/catalog"
)

// TestDemoTools checks that each call of the demo, run through the demo's
// tools as the agent runs a call, after the model streamed it, ends in the
// call's own last state, a call of a tool that is not built in included,
// and that a call stopped while it runs fails as interrupted.
func TestDemoTools(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		m := &Model{}
		tools := Tools(m)
		for _, seg := range Segments {
			if seg.Tool == nil {
				continue
			}
			m.Call = seg.Tool
			res := tools.Execute(t.Context(), agent.ToolCall{ID: "c", Name: seg.Tool.Name, Args: seg.Tool.Input})
			got := agent.ToolStateFor(res, seg.Tool.Input, time.Time{})
			if want := seg.Tool.State(time.Time{}, time.Time{}); !sameEnd(got, want) {
				t.Errorf("%s %s ended as %+v, want %+v", seg.Tool.Name, seg.Tool.Input, got, want)
			}
		}

		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		m.Call = Segments[0].Tool
		res := tools.Execute(ctx, agent.ToolCall{ID: "c", Name: m.Call.Name, Args: m.Call.Input})
		if !res.IsError || res.Content != message.InterruptedError {
			t.Errorf("stopped call = %+v, want it interrupted", res)
		}
	})
}

// sameEnd reports whether two last states of a call end the same way, with
// the same output or error, whenever they started and ended.
func sameEnd(a, b message.ToolState) bool {
	switch a := a.(type) {
	case message.ToolStateCompleted:
		b, ok := b.(message.ToolStateCompleted)
		return ok && a.Output == b.Output
	case message.ToolStateError:
		b, ok := b.(message.ToolStateError)
		return ok && a.Error == b.Error
	}
	return false
}

// TestDemoTurn checks that the agent loop, run with the demo's model and
// tools as the demo runs it, answers a prompt with every step of the demo,
// in order, ending with the last, which calls no tool, and that each call
// ends in its own last state and each reply carries an estimate that reads
// more from the cache as the conversation grows.
func TestDemoTurn(t *testing.T) {
	cat := agent.ProviderOptions{Catalog: catalog.NewStore(gateway.StoreOptions{Dir: t.TempDir()})}
	synctest.Test(t, func(t *testing.T) {
		model := &Model{Steps: Segments}
		tools := Tools(model)
		var ended []message.Content
		run := &agent.Agent{
			Providers:       agent.NewProviders(cat, model),
			Tools:           tools,
			Variant:         agent.Variant{Provider: Provider, Model: ModelName},
			Messages:        []agent.Message{agent.NewUserMessage(Prompts[0])},
			ToolDefinitions: tools.WireTools(),
			MaxSteps:        len(model.Steps),
			OnEvent: func(ev agent.Event) {
				if e, ok := ev.(agent.MessageEnded); ok {
					ended = append(ended, e.Message)
				}
			},
		}
		if err := run.Run(t.Context()); err != nil {
			t.Fatalf("the demo turn ended with %v", err)
		}
		if len(ended) != len(Segments) {
			t.Fatalf("the turn ended %d replies, want one for each of the %d steps", len(ended), len(Segments))
		}
		cached := 0
		for i, msg := range ended {
			seg := Segments[i]
			call := slices.IndexFunc(msg.Parts, func(p message.ContentPart) bool { _, ok := p.(message.ToolPart); return ok })
			switch {
			case seg.Tool == nil && call >= 0:
				t.Errorf("reply %d calls a tool, want its text alone", i)
			case seg.Tool != nil && call < 0:
				t.Errorf("reply %d calls no tool, want %s", i, seg.Tool.Name)
			case seg.Tool != nil:
				if got, want := msg.Parts[call].(message.ToolPart).State, seg.Tool.State(time.Time{}, time.Time{}); !sameEnd(got, want) {
					t.Errorf("%s %s ended as %+v, want %+v", seg.Tool.Name, seg.Tool.Input, got, want)
				}
			}
			u := msg.Usage()
			if u.InputTokens < BaseTokens || u.CachedInputTokens < cached || u.CacheCreationInputTokens != 0 {
				t.Errorf("reply %d usage = %+v, want an estimate reading at least %d from the cache", i, u, cached)
			}
			cached = u.CachedInputTokens
		}
		if cached == 0 {
			t.Error("no reply of the turn read from the cache")
		}
	})
}
