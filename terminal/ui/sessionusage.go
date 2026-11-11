package ui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/fluid/style"
)

// CacheText returns the cache use of a request that reported u in brackets,
// as the reply it made shows it after its last part: the prompt tokens it
// read from a prompt cache and those it wrote to one, such as "[cache 7.7K
// write 0K]". A request that read and wrote none shows both as 0K. It returns
// "" only for a reply that reported no usage at all, as one still streaming.
func CacheText(u message.Usage) string {
	if u.IsZero() {
		return ""
	}
	return fmt.Sprintf("[cache %s write %s]", FormatTokens(u.CachedInputTokens), FormatTokens(u.CacheCreationInputTokens))
}

// Context shares of the window above which the slash menu's status row
// warns that the context is filling up, and then that it is nearly full.
const (
	contextWarnPercent   = 70
	contextDangerPercent = 90
)

// SessionUsage is the token use of one reply, as UsageOf counts it, or of a
// transcript, as SumUsage totals it: its token counts, which price it, what
// its requests cost at API prices, and how much of the context window the
// latest one filled, which the slash menu's status row shows.
type SessionUsage struct {
	// Input counts the prompt tokens processed anew, apart from those read
	// from or written to a prompt cache.
	Input int
	// Output counts the output tokens.
	Output int
	// CacheRead counts the prompt tokens read from a prompt cache.
	CacheRead int
	// CacheWrite counts the prompt tokens written to a prompt cache.
	CacheWrite int
	// Cost is what the requests cost at API prices, in US dollars.
	Cost float64
	// Context counts the tokens of the latest request and its reply, which
	// the prompt of the next request starts from.
	Context int
	// Window is the most tokens a prompt of the terminal's model holds, and
	// zero when the catalog does not list the model.
	Window int
}

// UsageOf returns the token use of one reply that reported u, without cost
// or window: its counts, and its tokens, which the next prompt starts from.
func UsageOf(u message.Usage) SessionUsage {
	prompt := u.InputTokens + u.CacheCreationInputTokens
	return SessionUsage{
		Input:      max(0, u.InputTokens-u.CachedInputTokens),
		Output:     u.OutputTokens,
		CacheRead:  u.CachedInputTokens,
		CacheWrite: u.CacheCreationInputTokens,
		Context:    cmp.Or(u.TotalTokens, prompt+u.OutputTokens),
	}
}

// SumUsage totals the usage of the agent messages of msgs and prices
// each with the cost that price returns for it, which depends on its
// provider, model and mode, leaving out the messages it reports no cost for.
// The latest message with usage gives the context.
func SumUsage(msgs []ChatMessage, price func(ChatMessage) (gateway.Price, bool)) SessionUsage {
	var s SessionUsage
	for _, m := range msgs {
		if m.Role != message.RoleAgent || m.Usage.IsZero() {
			continue
		}
		r := UsageOf(m.Usage)
		s.Input += r.Input
		s.Output += r.Output
		s.CacheRead += r.CacheRead
		s.CacheWrite += r.CacheWrite
		if cost, ok := price(m); ok {
			prompt := m.Usage.InputTokens + m.Usage.CacheCreationInputTokens
			s.Cost += cost.At(prompt).Charge(r.Input, r.CacheRead, r.CacheWrite, r.Output)
		}
		s.Context = r.Context
	}
	return s
}

// labelEfforts are the reasoning efforts that SplitEffort sets apart from
// the model.
var labelEfforts = []agent.ReasoningEffort{
	agent.EffortNone, agent.EffortMinimal, agent.EffortLow, agent.EffortMedium,
	agent.EffortHigh, agent.EffortXHigh, agent.EffortMax,
}

// ModelName returns model, a variant in "provider/model:option:effort"
// format, as the /usage notice shows it: without its provider, and with a space in place of the colon before its
// reasoning effort, such as "model-a:fast medium". A colon before
// anything else is part of the model ID, as in a fine-tuned model's, and
// stays.
func ModelName(model string) string {
	_, ref := agent.ParseModel(model)
	if name, effort := SplitEffort(ref); effort != "" {
		return name + " " + string(effort)
	}
	return ref
}

// SplitEffort returns ref, a variant without its provider in
// "model:option:effort" form, apart from the reasoning effort it ends in,
// such as "model-a:fast" and "medium" of "model-a:fast:medium", or ref whole
// and no effort when it ends in none. A colon before anything else is part
// of the model ID, as in a fine-tuned model's, and stays.
func SplitEffort(ref string) (string, agent.ReasoningEffort) {
	if i := strings.LastIndexByte(ref, ':'); i >= 0 && slices.Contains(labelEfforts, agent.ReasoningEffort(ref[i+1:])) {
		return ref[:i], agent.ReasoningEffort(ref[i+1:])
	}
	return ref, ""
}

// ContextChunk returns the context the latest request of u used, such as
// "18K 2%": its tokens, followed by their share of the window, rounded to a
// whole percent, when the window is known. It shows in fg, or in th's warning
// color above 70% of the window and its danger color above 90%. Before any
// request it shows the context as none, such as "0K 0%".
func ContextChunk(u SessionUsage, fg style.Color, th *style.Theme) style.TextChunk {
	context := FormatTokens(u.Context)
	if u.Window > 0 {
		context += " " + ContextPercent(u)
	}
	if warn, ok := ContextWarnFg(u, th); ok {
		fg = warn
	}
	return style.New().WithFg(fg).Func()(context)
}

// ContextPercent returns the share of u's window its context fills,
// rounded to a whole percent, such as "2%", or "" when the window is not
// known.
func ContextPercent(u SessionUsage) string {
	if u.Window <= 0 {
		return ""
	}
	return fmt.Sprintf("%.0f%%", float64(u.Context)/float64(u.Window)*100)
}

// ContextWarnFg returns th's warning color when u's context fills more than
// 70% of its window, and its danger color above 90%, and reports false
// below that or when the window is not known.
func ContextWarnFg(u SessionUsage, th *style.Theme) (style.Color, bool) {
	if u.Window <= 0 {
		return style.Color{}, false
	}
	th = themeOrDefault(th)
	switch percent := float64(u.Context) / float64(u.Window) * 100; {
	case percent > contextDangerPercent:
		return th.Danger, true
	case percent > contextWarnPercent:
		return th.Warning, true
	}
	return style.Color{}, false
}

// statusGap parts the fields of StatusLine.
const statusGap = " "

// Labels of the session's prompt tokens processed anew and its output tokens
// in StatusLine.
const (
	statusInputLabel  = "↓"
	statusOutputLabel = "↑"
)

// StatusLine returns the status line the slash menu shows above the input
// box, such as "Lab A Model A Medium Context 64K 7% ↓ 120K ↑ 8.4K Cache 310K
// $0.42 Worked 3m 12s": the model by name, as DisplayName names it, or a dim
// StatusNone when name is empty, the
// context as ContextChunk gives it, the session's prompt tokens processed
// anew, its output tokens, and its prompt tokens read from a cache, which u
// totals, what they cost at API prices, and worked, how long the session's
// turns took. Values show in titleFg, as the slash menu's commands do, after
// their labels in mutedFg, as its descriptions do, statusGap apart.
func StatusLine(name string, u SessionUsage, worked time.Duration, th *style.Theme) style.StyledText {
	text := style.New().WithFg(titleFg).Func()
	dim := style.New().WithFg(mutedFg).Func()
	model := text(name)
	if name == "" {
		model = dim(StatusNone)
	}
	chunks := []style.TextChunk{
		model, style.Plain(statusGap), dim("Context "), ContextChunk(u, titleFg, th),
	}
	for _, f := range []struct{ label, value string }{
		{statusInputLabel, FormatTokens(u.Input)},
		{statusOutputLabel, FormatTokens(u.Output)},
		{"Cache", FormatTokens(u.CacheRead)},
		{"", FormatCost(u.Cost)},
		{"Worked", FormatDuration(worked)},
	} {
		chunks = append(chunks, style.Plain(statusGap))
		if f.label != "" {
			chunks = append(chunks, dim(f.label+" "))
		}
		chunks = append(chunks, text(f.value))
	}
	return style.NewStyledText(chunks...)
}
