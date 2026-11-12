package app

import (
	"errors"
	"fmt"
	"html"
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

// DefaultSystemPrompt is the instructions every session opens with, untagged,
// before the files of instructions and the configuration's
// agent.instructions, as SystemInstruction joins them. It is empty: a
// session's system prompt is what those files and the configuration say, and
// nothing of flow's own. The tools the model may call are stored beside it, in the
// same system message, as emitSystem stores them.
var DefaultSystemPrompt = ""

// InstructionsFile is the usual name of the file of instructions that the
// system prompt holds: the user's own, in flow's data directory, and the
// project's, as FindInstructions finds them. InstructionsFiles names the
// others a folder may hold in its place.
const InstructionsFile = "AGENTS.md"

// InstructionsOverrideFile is the name of a file of instructions that takes
// the place of the InstructionsFile of the same folder, and of no other.
const InstructionsOverrideFile = "AGENTS.override.md"

// InstructionsFiles are the names of the files of instructions that
// FindInstructions looks for in a folder, in the order it tries them: of
// those a folder holds, the first that is not blank is the folder's file.
var InstructionsFiles = [...]string{InstructionsOverrideFile, InstructionsFile}

// InstructionsTag is the tag of each section of the system prompt: one for
// each file of instructions, whose path attribute names the file, and then
// one, without a path, for the configuration's agent.instructions, or the
// app's Options.Instructions in its place.
const InstructionsTag = "instructions"

// Instruction is a file of instructions the system prompt holds.
type Instruction struct {
	// Path is the file's absolute path.
	Path string
	// Text is the file's content, without the blank lines around it.
	Text string
}

// Tagged returns the file's Text in an InstructionsTag tag whose path
// attribute is Path, escaped, each tag on a line of its own.
func (in Instruction) Tagged() string {
	return fmt.Sprintf("<%s path=\"%s\">\n%s\n</%s>", InstructionsTag, html.EscapeString(in.Path), strings.TrimSpace(in.Text), InstructionsTag)
}

// Section returns text, without the blank space around it, in a tag of
// name, each tag on a line of its own, or "" when text is blank.
func Section(name, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return "<" + name + ">\n" + text + "\n</" + name + ">"
}

// SystemInstruction returns the text of the system message a session opens
// with, each piece a blank line after the one before it: DefaultSystemPrompt
// as it is, then each of instructions as Tagged writes it, and then extra,
// the configuration's agent.instructions, in an InstructionsTag section
// without a path. A blank instruction is left out, as is a blank piece, so
// the text is empty when every piece is.
func SystemInstruction(instructions []Instruction, extra string) string {
	pieces := []string{strings.TrimSpace(DefaultSystemPrompt)}
	for _, in := range instructions {
		if strings.TrimSpace(in.Text) != "" {
			pieces = append(pieces, in.Tagged())
		}
	}
	pieces = append(pieces, Section(InstructionsTag, extra))
	return strings.Join(slices.DeleteFunc(pieces, func(p string) bool { return p == "" }), "\n\n")
}

// FindInstructions returns the files of instructions the system prompt
// holds, in this order: the user's own, in dataDir, flow's data directory,
// and then the project's, the nearest to workingDir, in it or in a folder
// above it up to the root of the git repository that holds it. Outside a
// repository only workingDir itself is looked in, so that a file of an
// unrelated folder above it is never read. In each folder it tries the
// names of InstructionsFiles in order and reads the first file it finds, so
// that an InstructionsOverrideFile takes the place of the folder's
// InstructionsFile. A path read already, as when the project is the data
// directory, is not read again. A file that is missing or blank is left
// out, so that the next name, or the next folder, is tried, and one that
// cannot be read is logged and left out, so that it never keeps a prompt
// from being sent. Either directory may be empty, for none.
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
	// readDir reads the first file of InstructionsFiles in dir, and reports
	// whether there is one.
	readDir := func(dir string) bool {
		for _, name := range InstructionsFiles {
			if read(filepath.Join(dir, name)) {
				return true
			}
		}
		return false
	}
	if dataDir != "" {
		if abs, err := filepath.Abs(dataDir); err == nil {
			readDir(abs)
		}
	}
	for dir := range ProjectDirs(workingDir) {
		if readDir(dir) {
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
// publishes as EventSessionUpdated. Its text is SystemInstruction's, of the
// files of instructions FindInstructions finds, in flow's data directory
// and the project, unless the app's Options.NoInstructionFiles leaves them
// out, and the configuration's agent.instructions, or the app's
// Options.Instructions in its place. Its message.ToolSetPart, after the
// text, holds tools, the definitions the run sends, when there are any. When
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
	if !s.app.noInstructionFiles {
		instructions = FindInstructions(s.app.config.Dir, s.app.workingDir)
	}
	extra := cfg.Agent.Instructions
	if s.app.instructions != "" {
		extra = s.app.instructions
	}
	text := SystemInstruction(instructions, extra)
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
