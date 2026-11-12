package app

import (
	"errors"
	"io/fs"
	"iter"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
)

// DefaultSystemPrompt is the instructions every session opens with, before
// the InstructionsFile files and the configuration's agent.system_prompt, as
// SystemPrompt joins them. It is empty: a session's system prompt is what
// those files and the configuration say, and nothing of flow's own. The
// tools the model may call are stored beside it, in the same system
// message, as emitSystem stores them.
var DefaultSystemPrompt = ""

// InstructionsFile is the name of the file of instructions that the system
// prompt holds: the user's own, in flow's data directory, and the project's,
// as FindInstructions finds them.
const InstructionsFile = "AGENTS.md"

// Instruction is a file of instructions the system prompt holds.
type Instruction struct {
	// Path is the file's absolute path.
	Path string
	// Text is the file's content, without the blank lines around it.
	Text string
}

// SystemPrompt returns the text of the system message a session opens with,
// each piece a blank line after the one before it: DefaultSystemPrompt, the
// text of each of instructions as the file holds it, and then extra, the
// configuration's agent.system_prompt. A blank piece is left out, so the
// text is empty when every piece is.
func SystemPrompt(instructions []Instruction, extra string) string {
	pieces := []string{strings.TrimSpace(DefaultSystemPrompt)}
	for _, in := range instructions {
		pieces = append(pieces, in.Text)
	}
	pieces = append(pieces, strings.TrimSpace(extra))
	return strings.Join(slices.DeleteFunc(pieces, func(p string) bool { return p == "" }), "\n\n")
}

// FindInstructions returns the InstructionsFile files the system prompt
// holds, in this order: the user's own, in dataDir, flow's data directory,
// and then the project's, the nearest to workingDir, in it or in a folder
// above it up to the root of the git repository that holds it. Outside a
// repository only workingDir itself is looked in, so that a file of an
// unrelated folder above it is never read. A path read already, as when
// the project is the data directory, is not read again. A file that is
// missing or blank is left out, and one that cannot be read is logged and
// left out, so that it never keeps a prompt from being sent. Either
// directory may be empty, for none.
func FindInstructions(dataDir, workingDir string) []Instruction {
	var out []Instruction
	read := func(path string) bool {
		if slices.ContainsFunc(out, func(in Instruction) bool { return in.Path == path }) {
			return true
		}
		body, err := os.ReadFile(path)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				slog.Warn("read instructions", "path", path, "err", err)
			}
			return false
		}
		text := strings.TrimSpace(string(body))
		if text == "" {
			return false
		}
		out = append(out, Instruction{Path: path, Text: text})
		return true
	}
	if dataDir != "" {
		if abs, err := filepath.Abs(dataDir); err == nil {
			read(filepath.Join(abs, InstructionsFile))
		}
	}
	for dir := range ProjectDirs(workingDir) {
		if read(filepath.Join(dir, InstructionsFile)) {
			break
		}
	}
	return out
}

// ProjectDirs yields the folders that hold workingDir's project, nearest
// first, by their absolute paths: workingDir, and each folder above it up
// to the root of the git repository that holds it, the folder with a .git
// entry. Outside a repository it yields workingDir alone, and for an empty
// workingDir nothing.
func ProjectDirs(workingDir string) iter.Seq[string] {
	return func(yield func(string) bool) {
		if workingDir == "" {
			return
		}
		start, err := filepath.Abs(workingDir)
		if err != nil {
			return
		}
		var dirs []string
		for dir := start; ; {
			dirs = append(dirs, dir)
			if _, err := os.Lstat(filepath.Join(dir, ".git")); err == nil {
				break
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				// No repository holds workingDir.
				dirs = dirs[:1]
				break
			}
			dir = parent
		}
		for _, dir := range dirs {
			if !yield(dir) {
				return
			}
		}
	}
}

// emitSystem stores the system message that opens the conversation, and
// publishes it as a prompt is published, when the session's head is empty,
// so that the next prompt would open the history: a new session's first
// prompt, or one sent after a checkout of the first message. Stored at the
// root, the message is the first of the history every run of the branch
// sends, as the transcript shows it. The system message is the first
// message of a new session, so it stores the session too, which it then
// publishes as EventSessionUpdated. Its text is SystemPrompt's, of the
// InstructionsFile files FindInstructions finds, in flow's data directory
// and the project, unless the app's Options.NoInstructions leaves them out,
// and the configuration's agent.system_prompt, or the app's
// Options.SystemPrompt in its place. Its message.ToolSetPart, after the text,
// holds tools, the definitions the run sends, when there are any. When
// there is neither, there is nothing to send, and it stores none. It fails
// when the config file cannot be read.
//
// A session whose head rests on a message already has its history, and its
// system prompt; emitSystem then stores tools as emitTools does, when they
// differ from those in force.
func (s Session) emitSystem(tools []agent.ToolDefinition) error {
	h, err := s.head()
	if err != nil {
		return err
	}
	if h.ID != "" {
		return s.emitTools(tools)
	}
	cfg, err := s.app.config.Get()
	if err != nil {
		return err
	}
	var instructions []Instruction
	if !s.app.noInstructions {
		instructions = FindInstructions(s.app.config.Dir, s.app.workingDir)
	}
	extra := cfg.Agent.SystemPrompt
	if s.app.systemPrompt != "" {
		extra = s.app.systemPrompt
	}
	text := SystemPrompt(instructions, extra)
	if text == "" && len(tools) == 0 {
		return nil
	}
	var parts []message.ContentPart
	if text != "" {
		parts = append(parts, message.TextPart{ID: message.NewPartID(), Text: text})
	}
	if len(tools) > 0 {
		parts = append(parts, toolSetPart(tools))
	}
	return s.storeSystem(parts, "instructions", len(instructions), "tools", len(tools))
}

// emitTools stores tools, the definitions a run sends, as a system message
// below the session's head, and publishes it as emitSystem does, when they
// differ from those in force at the head, as agent.ToolsIn finds them, so
// that the stored tools follow the run's: a set of tools that changed, as a
// build, an extension or a --tools flag changes it, and the first set of a
// session stored before tools were. The message holds the new set alone, an
// empty one when the run has no tools left, and no text: the system prompt
// stays the one the session opened with. A session that holds no set and
// whose run has no tools stores none.
func (s Session) emitTools(tools []agent.ToolDefinition) error {
	history, err := s.historyFromLog()
	if err != nil {
		return err
	}
	inForce, ok := agent.ToolsIn(history)
	if (ok && agent.SameTools(inForce, tools)) || (!ok && len(tools) == 0) {
		return nil
	}
	return s.storeSystem([]message.ContentPart{toolSetPart(tools)}, "tools", len(tools), "replaced", len(inForce))
}

// toolSetPart returns tools as the part of a system message that holds them,
// in their order.
func toolSetPart(tools []agent.ToolDefinition) message.ToolSetPart {
	return message.ToolSetPart{ID: message.NewPartID(), Tools: slices.Clone(tools)}
}

// storeSystem stores a system message of parts below the session's head,
// logged with attrs, and publishes it as a prompt is published. A session
// the store did not hold, as a new one, is stored with it, and published as
// EventSessionUpdated.
func (s Session) storeSystem(parts []message.ContentPart, attrs ...any) error {
	msg := message.Content{ID: message.NewID(), Role: message.RoleSystem, Parts: parts}
	stored, err := s.app.sessions.Stored(s.ctx, s.id)
	if err != nil {
		return err
	}
	if err := s.app.sessions.AppendMessage(s.ctx, s.id, &msg); err != nil {
		return err
	}
	s.Logger().Info("system message stored", append([]any{"message", msg.ID}, attrs...)...)
	at, err := s.branchPositions([]message.Content{msg})
	if err != nil {
		return err
	}
	s.publish(EventMessageStarted{EventBase: s.eventBase(), Message: Message{Content: msg}})
	s.publish(EventMessageEnded{EventBase: s.eventBase(), Message: Message{Content: msg, Branch: at[msg.ID]}})
	if !stored {
		if err := s.publishStored(); err != nil {
			s.Logger().Warn("read the session the system message stored", "err", err)
		}
	}
	return nil
}
