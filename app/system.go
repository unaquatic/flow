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

	"github.com/unaquatic/flow/agent/message"
)

// DefaultSystemPrompt is the instructions every session opens with, before
// the InstructionsFile files and the configuration's agent.system_prompt, as
// SystemPrompt joins them. It is empty: a session's system prompt is what
// those files and the configuration say, and nothing of flow's own. The
// tools the model may call are sent apart from it, with each request.
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
// sends, as the transcript shows it. A session whose head rests on a
// message already has its history, with or without a system message, and
// gets none. The system message is the first message of a new session, so
// it stores the session too, which it then publishes as
// EventSessionUpdated. Its text is SystemPrompt's, of the InstructionsFile
// files FindInstructions finds, in flow's data directory and the project,
// and the configuration's agent.system_prompt; when that is empty, there is
// nothing to send, and it stores none. It fails when the config file cannot
// be read.
func (s Session) emitSystem() error {
	h, err := s.head()
	if err != nil || h.ID != "" {
		return err
	}
	cfg, err := s.app.config.Get()
	if err != nil {
		return err
	}
	instructions := FindInstructions(s.app.config.Dir, s.app.workingDir)
	text := SystemPrompt(instructions, cfg.Agent.SystemPrompt)
	if text == "" {
		return nil
	}
	msg := message.Content{
		ID:    message.NewID(),
		Role:  message.RoleSystem,
		Parts: []message.ContentPart{message.TextPart{ID: message.NewPartID(), Text: text}},
	}
	stored, err := s.app.sessions.Stored(s.ctx, s.id)
	if err != nil {
		return err
	}
	if err := s.app.sessions.AppendMessage(s.ctx, s.id, &msg); err != nil {
		return err
	}
	s.Logger().Info("system message stored", "message", msg.ID, "instructions", len(instructions))
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
