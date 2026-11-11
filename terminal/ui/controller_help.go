package ui

import (
	"time"

	"github.com/unaquatic/fluid/style"
)

// UsageTitle heads the notice /usage shows while prompts show in PromptBar.
const UsageTitle = "Usage"

// usageLabelWidth is the width of the label column of the notice /usage
// shows.
const usageLabelWidth = 13

// UsageInfo holds the facts /usage shows, as UsageLines lays them out.
type UsageInfo struct {
	// Model is the terminal's model variant, as ModelName names it.
	Model string
	// Usage is the session's token use and what it cost.
	Usage SessionUsage
	// Worked is how long the session's turns took.
	Worked time.Duration
}

// UsageLines returns the rows of the notice /usage shows, each a dim label
// and its value: the model, the context as ContextValue gives it, the prompt
// tokens processed anew, the output tokens, the prompt tokens read from and
// written to a cache, what the requests cost at API prices, and how long the
// turns took. A nil th uses the default theme.
func UsageLines(info UsageInfo, th *style.Theme) []style.StyledText {
	u := info.Usage
	model := style.Plain(ModelName(info.Model))
	if info.Model == "" {
		model = style.New().WithFg(themeOrDefault(th).TextMuted).Func()(StatusNone)
	}
	return LabeledLines([]LabeledRow{
		{"Model", []style.TextChunk{model}},
		{"Context", []style.TextChunk{ContextValue(u, th)}},
		{"Input", []style.TextChunk{style.Plain(FormatTokens(u.Input))}},
		{"Output", []style.TextChunk{style.Plain(FormatTokens(u.Output))}},
		{"Cache read", []style.TextChunk{style.Plain(FormatTokens(u.CacheRead))}},
		{"Cache write", []style.TextChunk{style.Plain(FormatTokens(u.CacheWrite))}},
		{"Cost", []style.TextChunk{style.Plain(FormatCost(u.Cost))}},
		{"Worked", []style.TextChunk{style.Plain(FormatDuration(info.Worked))}},
	}, usageLabelWidth, th)
}

// ShowUsage runs /usage: it shows the session's context, token use and cost
// in a notice titled UsageTitle, as UsageLines lays them out.
func (s *Service) ShowUsage() []UIEvent {
	info := UsageInfo{Model: s.state.Model, Usage: s.SessionUsage(), Worked: s.SessionWorked()}
	return s.AddNotice(Notice{Lines: UsageLines(info, nil), Surface: NoticeSystem, Title: UsageTitle})
}

// HelpTitle heads the notice /help shows while prompts show in PromptBar.
const HelpTitle = "Help"

// KeyHelp is a key the terminal takes outside its menus, and what it does,
// as /help lists it.
type KeyHelp struct {
	// Key names the key as the hints write it, such as "ctrl+o".
	Key string
	// Does says what it does.
	Does string
}

// Keys are the keys /help lists, in its order.
var Keys = []KeyHelp{
	{"enter", "Send the prompt, or queue it during a run"},
	{"shift+enter", "Start a new line"},
	{"alt+enter", "Send now, stopping the run"},
	{"esc", "Stop the run"},
	{"ctrl+c", "Clear the input, or quit when pressed twice"},
	{"ctrl+o", "Expand or collapse the tool calls"},
	{"ctrl+u", "Pin or unpin the usage box"},
	{"shift+tab", "Cycle the reasoning effort"},
	{"pgup/pgdn", "Scroll the transcript"},
}

// helpLabelWidth is the width of the label column of the notice /help
// shows: the widest command or key and a gap.
func helpLabelWidth(cmds []SlashCommand) int {
	width := 0
	for _, c := range cmds {
		width = max(width, len(c.Name.String()))
	}
	for _, k := range Keys {
		width = max(width, len(k.Key))
	}
	return width + 2
}

// HelpLines returns the rows of the notice /help shows: cmds, each by the
// name the user types and its description, then Keys, under a blank row,
// each by its key and what it does. Names and keys are in productFg; the
// rest keeps the notice's text color.
func HelpLines(cmds []SlashCommand) []style.StyledText {
	name := style.New().WithFg(productFg).Func()
	width := helpLabelWidth(cmds)
	row := func(label, text string) style.StyledText {
		return style.NewStyledText(name(PadWidth(label, width)), style.Plain(text))
	}
	lines := make([]style.StyledText, 0, len(cmds)+1+len(Keys))
	for _, c := range cmds {
		lines = append(lines, row(c.Name.String(), c.Desc))
	}
	lines = append(lines, style.StyledText{})
	for _, k := range Keys {
		lines = append(lines, row(k.Key, k.Does))
	}
	return lines
}

// ShowHelp runs /help: it lists the shown slash commands, as ShownCommands
// gives them, and the keys in a notice titled HelpTitle, as HelpLines lays
// them out.
func (s *Service) ShowHelp() []UIEvent {
	return s.AddNotice(Notice{Lines: HelpLines(s.ShownCommands()), Surface: NoticeSystem, Title: HelpTitle})
}
