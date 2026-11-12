package app

import (
	"cmp"
	"context"
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
// goroutine of its own that Close waits for, as title describes. It cancels
// the session's titling in progress, which p supersedes. It runs in the
// app's context, so a stop of the run does not cut it short.
func (s Session) startTitling(p extension.Prompt) {
	if _, ok := extension.Find[extension.TitleHook](s.app.extensions); !ok {
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
		sess.title(p)
	})
}

// title asks the title hooks, in order, for the session's title after
// prompt p, and sets the first title one returns, as SetTitle sets it,
// unless the session has it already, or a later prompt superseded p, which
// ends s.ctx. A hook that fails is logged, and the next is asked.
func (s Session) title(p extension.Prompt) {
	log := s.Logger()
	stored, err := s.app.sessions.Session(s.ctx, s.id)
	if err != nil {
		log.Warn("read the session to title", "err", err)
		return
	}
	history, err := s.historyThrough(p.MessageID)
	if err != nil {
		log.Warn("read the history to title the session with", "err", err)
		return
	}
	t := extension.Titling{Prompt: p, History: history, Title: stored.Title, Fork: s.fork(p, history)}
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

// fork returns the extension.Fork that asks on from prompt p, after
// history, the session's branch through it. Its request starts as a run's
// starts, with the same messages, tools, and cache key, so that the
// provider serves what they share from its prompt cache. It stores the
// messages it sends and the reply, with the usage the provider reported,
// as a fork of message.TypeTitle below p.
func (s Session) fork(p extension.Prompt, history []agent.Message) extension.Fork {
	return func(ctx context.Context, model string, msgs ...agent.Message) (*agent.Response, error) {
		v, err := s.app.Models().Resolve(cmp.Or(model, p.Model))
		if err != nil {
			return nil, err
		}
		tools, _ := agent.ToolsIn(history)
		resp, err := s.app.providers.Generate(ctx, v, agent.Request{
			Messages: slices.Concat(history, msgs),
			Tools:    tools,
			CacheKey: string(s.id),
		})
		if err != nil {
			return nil, err
		}
		stored := make([]*message.Content, 0, len(msgs)+1)
		for _, m := range msgs {
			stored = append(stored, &message.Content{ID: message.NewID(), Role: m.Role, Parts: m.Content, Provider: v.Provider, Model: v.Ref(), Type: message.TypeTitle})
		}
		reply := &message.Content{ID: message.NewID(), Role: message.RoleAgent, Parts: slices.Clone(resp.Message.Content), Provider: v.Provider, Model: v.Ref(), Type: message.TypeTitle}
		if u := message.Usage(resp.Usage); !u.IsZero() {
			reply.Parts = append(reply.Parts, message.UsagePart{ID: message.NewPartID(), Usage: u})
		}
		stored = append(stored, reply)
		if err := s.app.sessions.AppendFork(ctx, s.id, p.MessageID, stored...); err != nil {
			s.Logger().Warn("store the fork", "message", p.MessageID, "err", err)
		}
		return resp, nil
	}
}
