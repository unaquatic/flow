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

// labelEfforts are the reasoning efforts that ModelName sets apart from the
// model with a space.
var labelEfforts = []agent.ReasoningEffort{
	agent.EffortNone, agent.EffortMinimal, agent.EffortLow, agent.EffortMedium,
	agent.EffortHigh, agent.EffortXHigh, agent.EffortMax,
}

// ModelName returns model, a variant in "provider/model:option:effort"
// format, as the slash menu's status row shows it before the context:
// without its provider, and with a space in place of the colon before its
// reasoning effort, such as "model-a:fast medium". A colon before
// anything else is part of the model ID, as in a fine-tuned model's, and
// stays.
func ModelName(model string) string {
	_, name := agent.ParseModel(model)
	if i := strings.LastIndexByte(name, ':'); i >= 0 && slices.Contains(labelEfforts, agent.ReasoningEffort(name[i+1:])) {
		return name[:i] + " " + name[i+1:]
	}
	return name
}

// ContextChunk returns the context the latest request of u used, such as
// "18K 2%": its tokens, followed by their share of the window, rounded to a
// whole percent, when the window is known. It shows in fg, or in th's warning
// color above 70% of the window and its danger color above 90%. Before any
// request it shows the context as none, such as "0K 0%".
func ContextChunk(u SessionUsage, fg style.Color, th *style.Theme) style.TextChunk {
	th = themeOrDefault(th)
	context := FormatTokens(u.Context)
	if u.Window > 0 {
		percent := float64(u.Context) / float64(u.Window) * 100
		context += fmt.Sprintf(" %.0f%%", percent)
		switch {
		case percent > contextDangerPercent:
			fg = th.Danger
		case percent > contextWarnPercent:
			fg = th.Warning
		}
	}
	return style.New().WithFg(fg).Func()(context)
}

// statusGap parts the fields of StatusLine.
const statusGap = " "

// StatusLine returns the status line the slash menu shows above the input
// box, such as "model-a medium context 64K 7% in 120K out 8.4K cache 310K
// $0.42 worked 3m 12s": model as ModelName names it, the context as
// ContextChunk gives it, the session's prompt tokens processed anew, its
// output tokens, and its prompt tokens read from a cache, which u totals,
// what they cost at API prices, and worked, how long the session's turns
// took. Values show in titleFg, as the slash menu's commands do, after their
// labels in PanelDimFg, as its descriptions do, statusGap apart.
func StatusLine(model string, u SessionUsage, worked time.Duration, th *style.Theme) style.StyledText {
	text := style.New().WithFg(titleFg).Func()
	dim := style.New().WithFg(PanelDimFg).Func()
	chunks := []style.TextChunk{
		text(ModelName(model)), style.Plain(statusGap), dim("context "), ContextChunk(u, titleFg, th),
	}
	for _, f := range []struct{ label, value string }{
		{"in", FormatTokens(u.Input)},
		{"out", FormatTokens(u.Output)},
		{"cache", FormatTokens(u.CacheRead)},
		{"", FormatCost(u.Cost)},
		{"worked", FormatDuration(worked)},
	} {
		chunks = append(chunks, style.Plain(statusGap))
		if f.label != "" {
			chunks = append(chunks, dim(f.label+" "))
		}
		chunks = append(chunks, text(f.value))
	}
	return style.NewStyledText(chunks...)
}
