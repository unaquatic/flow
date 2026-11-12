package app

import (
	"cmp"
	"context"
	"math"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/extension"
)

// Titling is the titling in progress on one session, which a later prompt
// to the session cancels.
type Titling struct {
	// Cancel cancels the context the titling's hooks run in.
	Cancel context.CancelFunc
}

// startTitling asks the extension.TitleHook of each of the app's extensions
// for the title of the session that prompt p was just stored to, in a
// goroutine of its own that Close waits for, as title describes. It reads
// the text of the prompts on the session's branch, p's last, here, while
// the session's head is still p. It cancels the session's titling in
// progress, which p supersedes. The goroutine runs in the app's context,
// so a stop of the run does not cut it short. A branch whose prompts hold
// no text titles nothing.
func (s Session) startTitling(p extension.Prompt) {
	if _, ok := extension.Find[extension.TitleHook](s.app.extensions); !ok {
		return
	}
	prompts, err := s.promptTexts()
	if err != nil {
		s.Logger().Warn("read the prompts to title the session with", "err", err)
		return
	}
	if len(prompts) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(s.app.workerCtx)
	t := &Titling{Cancel: cancel}
	s.app.mu.Lock()
	if s.app.titlings == nil {
		s.app.titlings = make(map[message.SessionID]*Titling)
	}
	if prev := s.app.titlings[s.id]; prev != nil {
		prev.Cancel()
	}
	s.app.titlings[s.id] = t
	s.app.mu.Unlock()

	// A plain handle in ctx, since the run's observer is gone once the run
	// ends.
	sess := Session{app: s.app, id: s.id, ctx: ctx}
	s.app.wg.Go(func() {
		defer func() {
			s.app.mu.Lock()
			if s.app.titlings[s.id] == t {
				delete(s.app.titlings, s.id)
			}
			s.app.mu.Unlock()
			cancel()
		}()
		sess.title(p, prompts)
	})
}

// promptTexts returns the text of each prompt on the session's branch,
// oldest first, leaving out those with none.
func (s Session) promptTexts() ([]string, error) {
	msgs, _, err := s.app.sessions.Prompts(s.ctx, s.id, math.MaxInt32)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, m := range msgs {
		if text := strings.TrimSpace(promptText(m.Parts)); text != "" {
			out = append(out, text)
		}
	}
	return out, nil
}

// promptText returns the text of a stored prompt, its parts: its first text
// part, which the user wrote, ahead of the parts attached to it.
func promptText(parts []message.ContentPart) string {
	for _, part := range parts {
		if t, ok := part.(message.TextPart); ok {
			return t.Text
		}
	}
	return ""
}

// title asks the title hooks, in order, for the session's title after
// prompt p, with prompts, the text of the prompts on its branch, and sets
// the first title one returns, as SetTitle sets it, unless the session has
// it already, or a later prompt superseded p, which ends s.ctx. A hook that
// fails is logged, and the next is asked.
func (s Session) title(p extension.Prompt, prompts []string) {
	log := s.Logger()
	stored, err := s.app.sessions.Session(s.ctx, s.id)
	if err != nil {
		log.Warn("read the session to title", "err", err)
		return
	}
	t := extension.Titling{Prompt: p, Prompts: prompts, Title: stored.Title, Reply: s.reply(p, prompts)}
	for hook := range extension.Hooks[extension.TitleHook](s.app.extensions) {
		title, err := hook.Title(s.ctx, t)
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			log.Warn("title session", "extension", hook.Name(), "err", err)
			continue
		}
		if title = strings.TrimSpace(title); title == "" {
			continue
		}
		if title == t.Title {
			return
		}
		if _, err := s.SetTitle(title); err != nil {
			log.Warn("title session", "extension", hook.Name(), "err", err)
			return
		}
		log.Info("session titled", "extension", hook.Name(), "title", title)
		return
	}
}

// reply returns the extension.Reply that asks a model, afresh, to title
// the session after prompt p: its request is TitlingRequest's messages, of
// the system message it is given and prompts, the text of the prompts on
// the session's branch, as they are stored, with the tools their tool set
// leaves, which are none, and none of the session's conversation, under
// the session's cache key. It stores those messages and the reply, with
// the usage the provider reported, as a chain of message.TypeTitling of
// its own in the session: a root linked to no message of the
// conversation, nor to any other titling.
func (s Session) reply(p extension.Prompt, prompts []string) extension.Reply {
	return func(ctx context.Context, model, system string) (*agent.Response, error) {
		v, err := s.app.Models().Resolve(cmp.Or(model, p.Model))
		if err != nil {
			return nil, err
		}
		stored := TitlingRequest(system, prompts, v)
		var msgs []agent.Message
		for _, m := range stored {
			msgs = append(msgs, agent.Message{Role: m.Role, Content: m.Parts})
		}
		tools, _ := agent.ToolsIn(msgs)
		resp, err := s.app.providers.Generate(ctx, v, agent.Request{
			Messages: msgs,
			Tools:    tools,
			CacheKey: string(s.id),
		})
		if err != nil {
			return nil, err
		}
		reply := &message.Content{ID: message.NewID(), Role: message.RoleAgent, Parts: slices.Clone(resp.Message.Content), Provider: v.Provider, Model: v.Ref(), Type: message.TypeTitling}
		if u := message.Usage(resp.Usage); !u.IsZero() {
			reply.Parts = append(reply.Parts, message.UsagePart{ID: message.NewPartID(), Usage: u})
		}
		stored = append(stored, reply)
		if err := s.app.sessions.AppendFork(ctx, s.id, "", stored...); err != nil {
			s.Logger().Warn("store the titling", "message", p.MessageID, "err", err)
		}
		return resp, nil
	}
}

// TitlingRequest returns the messages of a titling's request, of
// message.TypeTitling, as the session stores them, in the form the
// conversation stores its own: a system message of system, in an
// InstructionsTag section, as SystemInstruction writes the configuration's
// instructions, and of an empty message.ToolSetPart, which leaves the model
// no tools; and a user message of prompts, separated by blank lines, of
// model variant v, as a prompt names the variant it was sent to. A blank
// system leaves the system message the tool set alone.
func TitlingRequest(system string, prompts []string, v agent.Variant) []*message.Content {
	var sys []message.ContentPart
	if text := Section(InstructionsTag, system); text != "" {
		sys = append(sys, message.TextPart{ID: message.NewPartID(), Text: text})
	}
	sys = append(sys, toolSetPart([]agent.ToolDefinition{}))
	user := []message.ContentPart{message.TextPart{ID: message.NewPartID(), Text: strings.Join(prompts, "\n\n")}}
	return []*message.Content{
		{ID: message.NewID(), Role: message.RoleSystem, Parts: sys, Type: message.TypeTitling},
		{ID: message.NewID(), Role: message.RoleUser, Parts: user, Provider: v.Provider, Model: v.Ref(), Type: message.TypeTitling},
	}
}
