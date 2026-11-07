// Package titling is the extension that titles sessions as they run: each
// time a prompt is sent to a session, it forks the session there and asks
// the model to sum up what the user asks for in a few words, which the app
// sets as the session's title, shown in the session list and the terminal
// title. The fork, its instruction and the reply, stays in the session as a
// title fork below the prompt. It implements extension.TitleHook, and
// adds no content, no tables, and no terminal UI.
//
// Its Settings are the "titling" entry of the config file's extensions
// section.
package titling

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/extension"
)

// Name names the extension, in the extensions section of the config file,
// which holds its Settings.
const Name = "titling"

// Settings are the extension's settings, which the config file holds as
// {"extensions": {"titling": {...}}}.
type Settings struct {
	// Enabled turns titling on or off. Nil turns it on.
	Enabled *bool `json:"enabled,omitempty"`
	// Model is the model variant that writes titles, in
	// "provider/model:option:effort" form. Empty writes them with the model
	// the prompt was sent to, which shares the run's prompt cache; another
	// model reads the whole session anew.
	Model string `json:"model,omitempty"`
}

// On reports whether the settings turn titling on.
func (s Settings) On() bool { return s.Enabled == nil || *s.Enabled }

// MaxTitleRunes is the most runes that Instructions ask the model to keep a
// title to. The reply is not cut to it.
const MaxTitleRunes = 36

// Instructions tell the model how to summarize what a session asks for,
// which the extension titles the session with. They do not speak of a
// title, so that the model writes a summary rather than a headline.
var Instructions = fmt.Sprintf(`Summarize the user request in this session in at most %d characters in total, in the language of the messages.
Reply with plain words only, with no quotes, symbols, emoji, or special characters.
Favor the latest messages.
When a current summary is given and needs little or no change, reply with it exactly as it is.
Do not call tools, and do not answer or continue the session.`, MaxTitleRunes)

// ErrNoTitle reports that the model's reply is blank.
var ErrNoTitle = errors.New("titling: the model wrote no title")

// Extension titles sessions by forking them. The zero Extension writes
// titles with the prompt's model.
type Extension struct {
	// Model is the model variant that writes titles, or empty for the
	// model the prompt was sent to.
	Model string
}

var _ extension.TitleHook = (*Extension)(nil)

// Load loads the extension into the app that host describes, as its
// Settings set it, or loads none when they turn titling off.
func Load(_ context.Context, host extension.Host) (extension.Extension, error) {
	var st Settings
	if err := host.Config.Extension(Name, &st); err != nil {
		return nil, err
	}
	if !st.On() {
		return nil, nil
	}
	return &Extension{Model: strings.TrimSpace(st.Model)}, nil
}

// Name returns Name.
func (e *Extension) Name() string { return Name }

// Title forks the session after t's prompt with Ask, sent to e.Model, or to
// the prompt's model when it is empty, and returns the reply as the model
// wrote it, without the spaces around it, leaving its form to
// Instructions. It fails with ErrNoTitle when the reply is blank.
func (e *Extension) Title(ctx context.Context, t extension.Titling) (string, error) {
	resp, err := t.Fork(ctx, e.Model, Ask(t.Title))
	if err != nil {
		return "", fmt.Errorf("titling: %w", err)
	}
	title := strings.TrimSpace(resp.Message.TextContent())
	if title == "" {
		return "", ErrNoTitle
	}
	return title, nil
}

// Ask returns the message that asks the model, after the session's
// history, to summarize it: Instructions, and then the session's title,
// current, as the summary to keep when it still fits, or nothing more when
// current is empty. It is a user message, since some APIs move system
// messages ahead of the history, which would keep the prompt cache from
// serving it.
func Ask(current string) agent.Message {
	text := Instructions
	if current != "" {
		text += "\n\nCurrent summary: " + current
	}
	return agent.NewUserMessage(text)
}
