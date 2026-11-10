package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/product"
)

// LastReply returns the text of the last reply in msgs that holds any: the
// text parts of the latest agent message with text, joined by blank lines,
// and false when no reply holds text.
func LastReply(msgs []ChatMessage) (string, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Notice != nil || m.Role != message.RoleAgent {
			continue
		}
		if text := PartsText(m.Parts); text != "" {
			return text, true
		}
	}
	return "", false
}

// PartsText returns the text parts of parts, each without the white space
// around it, joined by blank lines, leaving out the empty ones.
func PartsText(parts []message.ContentPart) string {
	var texts []string
	for _, p := range parts {
		if t, ok := p.(message.TextPart); ok {
			if text := strings.TrimSpace(t.Text); text != "" {
				texts = append(texts, text)
			}
		}
	}
	return strings.Join(texts, "\n\n")
}

// HandleCopy runs /copy: it asks the UI to put the last reply's text, as
// LastReply gives it, on the clipboard, or says there is none.
func (s *Service) HandleCopy() []UIEvent {
	text, ok := LastReply(s.state.Messages)
	if !ok {
		return s.ShowToast("No reply to copy")
	}
	return []UIEvent{EventClipboardCopy{Text: text}}
}

// CompleteCopy reports how the copy of a reply to the clipboard went: err is
// the failure to send it to the terminal, or nil.
func (s *Service) CompleteCopy(err error) []UIEvent {
	if err != nil {
		return s.ShowToast(fmt.Sprintf("Copy failed: %v", err))
	}
	return s.ShowToast("Copied the last reply to the clipboard")
}

// ExportName returns the name of the file /export saves session id to, such
// as "flow-01a0e33d.md".
func ExportName(id message.SessionID) string {
	return product.Name + "-" + string(id) + ".md"
}

// HandleExport runs /export: it saves the chat as Markdown, as
// TranscriptMarkdown writes it, under the session's title, to a file named
// as ExportName names it in the working directory, replacing an earlier
// export of the session, and says where.
func (s *Service) HandleExport() []UIEvent {
	id := s.SessionID()
	if id == "" || s.MessageCount() == 0 {
		return s.ShowToast("Nothing to export")
	}
	title := ""
	if stored, err := s.app.Sessions().Session(s.ctx, id); err == nil {
		title = stored.Title
	}
	path := filepath.Join(s.app.WorkingDir(), ExportName(id))
	if err := os.WriteFile(path, []byte(TranscriptMarkdown(title, s.state.Messages)), 0o644); err != nil {
		return s.ShowToast(fmt.Sprintf("Export failed: %v", err))
	}
	return s.ShowKeywordToast("Exported the chat to ", RelPathForToast(path), "")
}

// TranscriptMarkdown returns msgs as a Markdown document headed by title, or
// UntitledName: a heading for each run of messages of one role, followed by
// their text, and for each tool call its name, its input and its output or
// error, fenced. Reasoning, notices, and parts that hold no text are left
// out; an image or a file a prompt attaches is named in brackets.
func TranscriptMarkdown(title string, msgs []ChatMessage) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n", SessionTitle(title))
	var role *message.Role
	for _, m := range msgs {
		if m.Notice != nil {
			continue
		}
		if role == nil || m.Role != *role {
			role = &m.Role
			fmt.Fprintf(&b, "\n## %s\n", Capitalize(m.Role.String()))
		}
		for _, p := range m.Parts {
			writePartMarkdown(&b, p)
		}
	}
	return b.String()
}

// writePartMarkdown writes p to b as TranscriptMarkdown shows it, after a
// blank line, or nothing for a part it leaves out.
func writePartMarkdown(b *strings.Builder, p message.ContentPart) {
	switch p := p.(type) {
	case message.TextPart:
		if text := strings.TrimSpace(p.Text); text != "" {
			fmt.Fprintf(b, "\n%s\n", text)
		}
	case message.ImagePart:
		b.WriteString("\n[image]\n")
	case message.FilePart:
		fmt.Fprintf(b, "\n[file %s]\n", cmp.Or(p.Filename, p.MediaType))
	case message.ToolPart:
		fmt.Fprintf(b, "\n### %s\n", p.Name)
		if in := strings.TrimSpace(string(p.Input())); in != "" {
			b.WriteString("\n" + Fence(in, "json"))
		}
		switch st := p.State.(type) {
		case message.ToolStateCompleted:
			if out := strings.TrimSpace(st.Output); out != "" {
				b.WriteString("\n" + Fence(out, ""))
			}
		case message.ToolStateError:
			b.WriteString("\n" + Fence("Error: "+st.Error, ""))
		}
	}
}

// Fence returns text as a fenced Markdown code block tagged info, its fence
// a run of backticks longer than any in text, and at least three.
func Fence(text, info string) string {
	longest, run := 0, 0
	for _, r := range text {
		if r != '`' {
			run = 0
			continue
		}
		run++
		longest = max(longest, run)
	}
	fence := strings.Repeat("`", max(3, longest+1))
	return fence + info + "\n" + text + "\n" + fence + "\n"
}
