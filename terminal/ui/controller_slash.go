package ui

import (
	"iter"
	"slices"
	"strings"
	"unicode"

	"github.com/unaquatic/fluid/widget"
)

// Command is a slash command's name, without its slash, such as "status".
type Command string

// The slash commands.
const (
	CommandResume   Command = "resume"
	CommandModel    Command = "model"
	CommandEffort   Command = "effort"
	CommandNew      Command = "new"
	CommandTree     Command = "tree"
	CommandContinue Command = "continue"
	CommandUndo     Command = "undo"
	CommandRedo     Command = "redo"
	CommandStatus   Command = "status"
	CommandSettings Command = "settings"
	CommandLogin    Command = "login"
	CommandQuit     Command = "quit"
)

// commandPrefix starts every slash command.
const commandPrefix = "/"

// String returns c as the user types it, such as "/status".
func (c Command) String() string {
	return commandPrefix + string(c)
}

// CutCommand returns text without the slash that starts a command, and
// reports whether text starts with one.
func CutCommand(text string) (string, bool) {
	return strings.CutPrefix(text, commandPrefix)
}

// SlashCommand is a slash command: its entry in the menu and what it does.
type SlashCommand struct {
	// Name is the command.
	Name Command
	// Aliases are other names that run the command when typed and find it
	// in the menu, such as "history" for /resume. Desc contains each.
	Aliases []Command
	// Desc is the description shown beside the command.
	Desc string
	// Run performs the command. The input is cleared before it runs.
	Run func(s *Service) []UIEvent
}

// Names yields the command's name and then its aliases.
func (c SlashCommand) Names() iter.Seq[Command] {
	return func(yield func(Command) bool) {
		if !yield(c.Name) {
			return
		}
		for _, alias := range c.Aliases {
			if !yield(alias) {
				return
			}
		}
	}
}

// SlashCommandGroups are the slash commands in groups, in the order the menu
// lists them: the session and its model, moves through its history, and the
// app.
var SlashCommandGroups = [][]SlashCommand{
	{
		{Name: CommandResume, Aliases: []Command{"session", "history"},
			Desc: "Resume a previous session from history", Run: (*Service).HandleSessionList},
		{Name: CommandNew, Aliases: []Command{"clear"},
			Desc: "Clear the chat and start a new conversation", Run: (*Service).ClearMessages},
		{Name: CommandModel, Desc: "Switch the model", Run: (*Service).ShowModelMenu},
		{Name: CommandEffort, Desc: "Change the reasoning effort", Run: (*Service).HandleEffort},
		{Name: CommandStatus, Aliases: []Command{"info"},
			Desc: "Show the version and session info", Run: (*Service).ShowStatus},
	},
	{
		{Name: CommandUndo, Desc: "Move the workspace back one turn", Run: (*Service).HandleUndo},
		{Name: CommandRedo, Desc: "Move the workspace forward one turn", Run: (*Service).HandleRedo},
		{Name: CommandContinue, Desc: "Pick the work up from where the chat ends", Run: (*Service).HandleContinue},
		{Name: CommandTree, Desc: "Jump to any point in the session tree", Run: (*Service).ShowTree},
	},
	{
		{Name: CommandSettings, Aliases: []Command{"config"},
			Desc: "Change settings in the config", Run: (*Service).ShowSettingsMenu},
		{Name: CommandLogin, Aliases: []Command{"auth"},
			Desc: "Authenticate with a provider account", Run: (*Service).ShowLoginMenu},
		{Name: CommandQuit, Aliases: []Command{"exit"}, Desc: "Exit", Run: (*Service).Quit},
	},
}

// SlashCommands are the UI's own slash commands, in the order the menu offers
// them.
var SlashCommands = slices.Concat(SlashCommandGroups...)

// CommandsWith returns the slash commands of a UI that runs with exts, in the
// order the menu offers them: SlashCommands, with the commands of each
// CommandExtension among exts, in order, before /quit, which ends the menu.
func CommandsWith(exts []Extension) []SlashCommand {
	var added []SlashCommand
	for _, e := range exts {
		if c, ok := e.(CommandExtension); ok {
			added = append(added, c.SlashCommands()...)
		}
	}
	if len(added) == 0 {
		return SlashCommands
	}
	quit := slices.IndexFunc(SlashCommands, func(c SlashCommand) bool { return c.Name == CommandQuit })
	return slices.Concat(SlashCommands[:quit], added, SlashCommands[quit:])
}

// LookupCommand returns the slash command among cmds named c, by its name or
// by one of its aliases.
func LookupCommand(cmds []SlashCommand, c Command) (SlashCommand, bool) {
	for _, sc := range cmds {
		for name := range sc.Names() {
			if name == c {
				return sc, true
			}
		}
	}
	return SlashCommand{}, false
}

// HandleSlashSelect runs the command chosen from the menu, as if it had been
// typed.
func (s *Service) HandleSlashSelect(cmd Command) []UIEvent {
	return slices.Concat(s.ClearInput(), s.HideSlashMenu(), s.HandleSubmit(cmd.String()))
}

// SlashMenuItems returns the commands matching query, the text typed after
// the slash, best first as MatchSlashCommands ranks them. Each item's value
// is its Command. An empty query lists every command, group after group,
// with no blank row between them. A command found by an alias shows as it
// always does: its description holds the alias. A query containing
// whitespace matches nothing, since no command takes arguments.
func (s *Service) SlashMenuItems(query string) []widget.SelectItem {
	if strings.ContainsFunc(query, unicode.IsSpace) {
		return nil
	}
	cmds := s.CommandList()
	if query != "" {
		cmds = MatchSlashCommands(cmds, query)
	}
	items := make([]widget.SelectItem, len(cmds))
	for i, cmd := range cmds {
		items[i] = cmd.Item()
	}
	return items
}

// MatchSlashCommands returns the slash commands among cmds that query matches
// by name or by alias, best first as [RankByName] ranks them. A command is
// ranked by its best match, and on a tie by its place in cmds.
func MatchSlashCommands(cmds []SlashCommand, query string) []SlashCommand {
	type named struct {
		cmd  SlashCommand
		name Command
	}
	var all []named
	for _, cmd := range cmds {
		for name := range cmd.Names() {
			all = append(all, named{cmd, name})
		}
	}
	var out []SlashCommand
	for _, n := range RankByName(query, all, func(n named) string { return string(n.name) }) {
		if !slices.ContainsFunc(out, func(c SlashCommand) bool { return c.Name == n.cmd.Name }) {
			out = append(out, n.cmd)
		}
	}
	return out
}

// Item returns c as a row of the slash menu, named as the user types it,
// such as "/status".
func (c SlashCommand) Item() widget.SelectItem {
	return widget.SelectItem{Name: c.Name.String(), Description: c.Desc, Value: c.Name}
}

// ShowSlashMenu offers the commands matching query, or hides the menu when
// none do.
func (s *Service) ShowSlashMenu(query string) []UIEvent {
	opts := s.SlashMenuItems(query)
	if len(opts) == 0 {
		return s.HideSlashMenu()
	}
	s.state.SlashMenuVisible = true
	return []UIEvent{EventSlashMenuShow{Items: opts}}
}

// HideSlashMenu dismisses the command menu.
func (s *Service) HideSlashMenu() []UIEvent {
	s.state.SlashMenuVisible = false
	return []UIEvent{EventSlashMenuHide{}}
}

// UpdateSlashMenu opens or closes the command menu as the input gains
// or loses its leading slash.
func (s *Service) UpdateSlashMenu(text string) []UIEvent {
	query, ok := CutCommand(text)
	if !ok {
		return s.HideSlashMenu()
	}
	return s.ShowSlashMenu(query)
}
