package main

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/config"
	"github.com/unaquatic/flow/product"
)

// demoRunModel is the default model of the run demos' apps, at
// demoLoginProvider.
const demoRunModel = "lab-a/model-a"

// demoRunAnswer is the final answer of the model in the answer case of the
// run demos, which a reply that reads calc.go comes before.
const demoRunAnswer = "TestAdd expects Add(2, 2) to be 4, but Add subtracts its second argument, so it returns 0.\n\n" +
	"Change the minus in calc.go to a plus and the test passes."

// demoMissingSession is the session ID of the run demo that names a
// session that is not stored.
const demoMissingSession = "0192f3c4-5a6b-7c8d-9e0f-1a2b3c4d5e09"

// runDemos are the cases of flow -p, each on a demo app of its own whose
// model replies as the case has it.
var runDemos = []demoCase{
	{name: "answer", desc: "A prompt, and the final answer alone, after the steps before it", line: `-p "why does TestAdd fail?"`,
		run: promptDemo(RunOptions{Format: OutputText}, "why does TestAdd fail?",
			demoReply(agent.TextDelta{Text: "Let me read calc.go."},
				agent.ToolUseStart{ID: "call_1", Name: "read"},
				agent.ToolUseEnd{ID: "call_1", Input: []byte(`{"path":"calc.go"}`)},
				agent.Stop{Reason: agent.StopToolUse}),
			demoReply(agent.TextDelta{Text: demoRunAnswer}, agent.Stop{Reason: agent.StopEndTurn}))},
	{name: "json", desc: "Each event of the run, as a line of JSON", line: `-p --format json "say hi"`,
		run: promptDemo(RunOptions{Format: OutputJSON}, "say hi", demoReply(agent.TextDelta{Text: "Hi!"}, agent.Stop{Reason: agent.StopEndTurn}))},
	{name: "failure", desc: "A model that refuses the request", line: `-p "say hi"`,
		run: failedPromptDemo(&agent.ResponseError{Provider: string(demoLoginProvider), StatusCode: 401, Message: "invalid API key"})},
	{name: "no-session", desc: "A run that keeps no session", line: `-p --no-session "say hi"`,
		run: promptDemo(RunOptions{Format: OutputText, NoSession: true}, "say hi", demoReply(agent.TextDelta{Text: "Hi!"}, agent.Stop{Reason: agent.StopEndTurn}))},
	{name: "missing-session", desc: "A session that is not stored", line: "-p --session " + demoMissingSession + ` "say hi"`,
		run: promptDemo(RunOptions{Format: OutputText, Session: demoMissingSession}, "say hi")},
	{name: "no-prompt", desc: "No prompt, as arguments or on standard input", line: "-p",
		run: func(context.Context, io.Writer, bool) error {
			_, err := promptText(nil, nil)
			return err
		}},
}

// promptDemo returns the run of a case of flow -p with options o, which
// sends prompt to a demo app whose model answers its requests with replies,
// one each, in order, and then with the last again. Where w is a terminal,
// each reply takes a while, as a model's does.
func promptDemo(o RunOptions, prompt string, replies ...agent.StreamResponse) func(context.Context, io.Writer, bool) error {
	return func(ctx context.Context, w io.Writer, _ bool) error {
		slow := newLiveLine(w).live
		n := 0
		reply := func() (agent.StreamResponse, error) {
			if len(replies) == 0 {
				return nil, errors.ErrUnsupported
			}
			if slow {
				time.Sleep(400 * time.Millisecond)
			}
			r := replies[min(n, len(replies)-1)]
			n++
			return r, nil
		}
		return withRunDemoApp(ctx, reply, func(a *app.App) error { return runPrompt(ctx, w, a, o, prompt) })
	}
}

// failedPromptDemo returns the run of a case of flow -p that sends a prompt
// to a demo app whose model fails each request with err.
func failedPromptDemo(err error) func(context.Context, io.Writer, bool) error {
	return func(ctx context.Context, w io.Writer, _ bool) error {
		reply := func() (agent.StreamResponse, error) { return nil, err }
		return withRunDemoApp(ctx, reply, func(a *app.App) error {
			return runPrompt(ctx, w, a, RunOptions{Format: OutputText}, "say hi")
		})
	}
}

// demoReply returns a stream that yields parts, in order.
func demoReply(parts ...agent.StreamPart) agent.StreamResponse {
	return func(yield func(agent.StreamPart, error) bool) {
		for _, p := range parts {
			if !yield(p, nil) {
				return
			}
		}
	}
}

// withRunDemoApp runs run on a demo app over a new temporary data directory,
// which downloads no catalog and loads no extension, whose default model is
// demoRunModel at demoLoginProvider, which answers each request with what
// reply returns, and then closes the app and removes the directory. The
// working directory is the data directory meanwhile, so that the run reads
// no instructions of the user's project. Nothing of the user's is read or
// changed.
func withRunDemoApp(ctx context.Context, reply func() (agent.StreamResponse, error), run func(*app.App) error) error {
	dir, err := os.MkdirTemp("", product.ID+"-demo-run-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	wd, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer os.Chdir(wd)
	settings := `{"default_provider":"` + string(demoLoginProvider) + `","default_model":"` + demoRunModel +
		`","snapshot":false,"catalog_source":"` + demoNowhere + `"}`
	if err := os.WriteFile(filepath.Join(dir, config.FileName), []byte(settings), 0o600); err != nil {
		return err
	}
	accounts := agent.NewAccounts(agent.AccountsConfig{Store: agent.AuthFile{Dir: dir}})
	store := gateway.NewStore(gateway.StoreOptions{Dir: dir, Source: demoNowhere, Snapshot: demoCatalog})
	providers := agent.NewProviders(agent.ProviderOptions{Catalog: store}, DemoReplyProvider{ID: demoLoginProvider, Reply: reply})
	a, err := app.New(ctx, config.NewService(dir), app.Options{Accounts: accounts, Providers: providers, BuiltIn: []agent.ProviderID{}})
	if err != nil {
		return err
	}
	defer a.Close()
	return run(a)
}

// DemoReplyProvider is the provider of a run demo's app, whose models answer
// each request with what Reply returns.
type DemoReplyProvider struct {
	// ID names the provider.
	ID agent.ProviderID
	// Reply returns the response to each request.
	Reply func() (agent.StreamResponse, error)
}

// Name returns the provider's ID.
func (p DemoReplyProvider) Name() string { return string(p.ID) }

// Model returns a model that answers each request as p does.
func (p DemoReplyProvider) Model(string) agent.Model { return DemoReplyModel{Reply: p.Reply} }

// DemoReplyModel is the model of every DemoReplyProvider.
type DemoReplyModel struct {
	// Reply returns the response to each request.
	Reply func() (agent.StreamResponse, error)
}

// Stream returns what Reply returns.
func (m DemoReplyModel) Stream(context.Context, agent.Request) (agent.StreamResponse, error) {
	return m.Reply()
}
