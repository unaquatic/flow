package app

import (
	"context"

	"github.com/unaquatic/flow/agent"
)

// Script is a run that its caller writes in place of a prompt: turns that
// each open with a prompt of the script's own, which the agent loop answers
// against the session's history, as it answers a run's prompt, but with the
// model and the tools of the script rather than the app's. It runs the hooks
// of no extension, so it takes no snapshots. The demo extension plays one.
type Script struct {
	// Provider is the provider of the model that answers, which the app adds
	// to its providers for the run.
	Provider agent.Provider
	// Variant names the model that answers, one of Provider's.
	Variant agent.Variant
	// Tools runs the calls the model makes, and are the tools each request
	// offers it.
	Tools *agent.Tools
	// MaxSteps, when positive, limits the responses of a turn. Zero sets no
	// limit.
	MaxSteps int
	// Next returns the prompt that opens turn i, the first being 0, which it
	// may wait for, and reports false to end the run, as when ctx, the run's
	// context, ends while it waits.
	Next func(ctx context.Context, i int) (string, bool)
}

// Play starts script, a run like Send's that stores its prompts and replies
// until Stop ends it or the script has no more prompts. It reports the
// errors Send does.
func (s Session) Play(script Script) error {
	return s.start(nil, func(s Session, ctx context.Context) error { return s.processScript(ctx, script) })
}

// processScript runs script until ctx, the run's context, ends, or the
// script has no more prompts: turns that each open with the prompt Next
// gives, which the agent loop answers against the session's history, as the
// store holds it once the prompt is stored, and the tools in force in it. A
// stop settles as a run's does.
func (s Session) processScript(ctx context.Context, script Script) error {
	providers := s.app.providers.WithProvider(script.Provider)
	for i := 0; ctx.Err() == nil; i++ {
		text, ok := script.Next(ctx, i)
		if !ok {
			return nil
		}
		if err := s.emitSystem(script.Tools.WireTools()); err != nil {
			return err
		}
		snd := SendInput{Text: text}
		if err := s.emitPrompt(snd, snd.content(), script.Variant, nil); err != nil {
			return err
		}
		history, err := s.historyFromLog()
		if err != nil {
			return err
		}
		tools, _ := agent.ToolsIn(history)
		run := &agent.Agent{
			Providers:       providers,
			Tools:           script.Tools,
			Variant:         script.Variant,
			Messages:        history,
			ToolDefinitions: tools,
			MaxSteps:        script.MaxSteps,
			SessionID:       s.id,
			OnEvent:         s.publishAgent,
			Store:           s.app.sessions,
		}
		if err := settle(ctx, run.Run(ctx)); err != nil {
			return err
		}
	}
	return nil
}
