package ui

import (
	"math"
	"slices"
	"testing"
	"time"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/extension/protocol/protocoltest"
	"github.com/unaquatic/fluid/style"
)

// usageReply returns an agent message of model, of lab-a, at provider-a,
// with usage u.
func usageReply(id message.ID, model string, u message.Usage) ChatMessage {
	return ChatMessage{ID: id, Role: message.RoleAgent, Provider: providerA, Model: "lab-a/" + model, Usage: u}
}

// TestSumUsage checks that the totals count the input apart from the cache,
// that only messages of a priced model add to the cost, and that the latest
// reply gives the context.
func TestSumUsage(t *testing.T) {
	read := 0.1
	price := func(m ChatMessage) (gateway.Price, bool) {
		if m.Model != "lab-a/model-x" {
			return gateway.Price{}, false
		}
		return gateway.Price{Rates: gateway.Rates{Input: 1, Output: 10, CacheRead: &read}}, true
	}
	msgs := []ChatMessage{
		userText("u1", "hi"),
		usageReply("a1", "model-x", message.Usage{InputTokens: 1000, OutputTokens: 100, TotalTokens: 1100}),
		usageReply("a2", "unpriced", message.Usage{InputTokens: 500, OutputTokens: 50, TotalTokens: 550}),
		usageReply("a3", "model-x", message.Usage{InputTokens: 3000, CachedInputTokens: 2000, OutputTokens: 200, TotalTokens: 3200}),
		// A reply that reported nothing, such as one stopped early, changes
		// nothing.
		usageReply("a4", "model-x", message.Usage{}),
	}
	got := SumUsage(msgs, price)
	want := SessionUsage{Input: 2500, Output: 350, CacheRead: 2000, Context: 3200}
	// a1 costs 1000×1 + 100×10, and a3 1000×1 + 2000×0.1 + 200×10, per
	// million tokens.
	wantCost := (2000.0 + 3200.0) / 1e6
	if math.Abs(got.Cost-wantCost) > 1e-12 {
		t.Errorf("cost = %v, want %v", got.Cost, wantCost)
	}
	got.Cost = 0
	if got != want {
		t.Errorf("usage = %+v\nwant    %+v", got, want)
	}
}

// TestSumUsageCacheWrites checks that tokens written to a cache count apart
// from the input, as the Messages format reports them, and in the context.
func TestSumUsageCacheWrites(t *testing.T) {
	msgs := []ChatMessage{usageReply("a1", "model-y", message.Usage{
		InputTokens: 100, CachedInputTokens: 50, CacheCreationInputTokens: 900, OutputTokens: 10,
	})}
	got := SumUsage(msgs, func(ChatMessage) (gateway.Price, bool) { return gateway.Price{}, false })
	want := SessionUsage{Input: 50, Output: 10, CacheRead: 50, CacheWrite: 900, Context: 1010}
	if got != want {
		t.Errorf("usage = %+v\nwant    %+v", got, want)
	}
}

// TestContextChunk checks that the context shows alone, without the cost or
// any of the token totals.
func TestContextChunk(t *testing.T) {
	tests := []struct {
		name  string
		usage SessionUsage
		want  string
	}{
		// Before any request the context shows as none, with its share of
		// the window when that is known.
		{name: "nothing yet", want: "0K"},
		{name: "window only", usage: SessionUsage{Window: 200_000}, want: "0K 0%"},
		{
			name: "context, not cost",
			usage: SessionUsage{Input: 27_000, Output: 1_100, CacheRead: 17_000,
				Cost: 0.0034, Context: 12_512, Window: 272_000},
			want: "13K 5%",
		},
		// Without a window the context shows without its share.
		{name: "model not in the catalog", usage: SessionUsage{Input: 1_200, Cost: 0.0123, Context: 1_676}, want: "1.7K"},
		{name: "no price", usage: SessionUsage{Input: 10, Context: 20, Window: 1_000}, want: "0.1K 2%"},
		// The share rounds to a whole percent.
		{name: "share rounded down", usage: SessionUsage{Context: 27_000, Window: 900_000}, want: "27K 3%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ContextChunk(tt.usage, mutedFg, nil).Text; got != tt.want {
				t.Errorf("context = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestModelName checks that the status row names a model without its provider,
// as its catalog ID,
// with its reasoning effort after a space, and keeps the colons of a model ID.
func TestModelName(t *testing.T) {
	for model, want := range map[string]string{
		"provider-a/lab-a/model-b":                     "lab-a/model-b",
		"provider-b/lab-b/model-d":                     "lab-b/model-d",
		"model-d":                                      "model-d",
		"provider-a/lab-a/model-b-fast:medium":         "lab-a/model-b-fast medium",
		"provider-b/lab-b/model-f:max":                 "lab-b/model-f max",
		"provider-a/ft:model-a:acme:support:a1b2":      "ft:model-a:acme:support:a1b2",
		"provider-a/ft:model-a:acme:support:a1b2:high": "ft:model-a:acme:support:a1b2 high",
	} {
		if got := ModelName(model); got != want {
			t.Errorf("ModelName(%q) = %q, want %q", model, got, want)
		}
	}
}

// TestDisplayName checks that a model reads as its variant's name and its
// effort's name, without its lab's, and that a model the catalog does not
// list reads as its model, after its lab, as unknown, and the name of its
// effort.
func TestDisplayName(t *testing.T) {
	cat := protocoltest.FromAPI(t, testCatalog)
	for model, want := range map[string]string{
		"provider-a/lab-a/model-a":             "Model A",
		"provider-a/lab-a/model-a:fast":        "Model A Fast",
		"provider-a/lab-a/model-a:fast:xhigh":  "Model A Fast Extra High",
		"provider-b/lab-b/model-f:medium":      "Model F Medium",
		"provider-a/lab-a/model-h:medium":      "Unknown [model-h] Medium",
		"provider-z/lab-z/model-z:high":        "Unknown [model-z] High",
		"provider-a/model-x":                   "Unknown [model-x]",
		"provider-a/ft:model-a:acme:a1b2":      "Unknown [ft:model-a:acme:a1b2]",
		"provider-a/ft:model-a:acme:a1b2:high": "Unknown [ft:model-a:acme:a1b2] High",
	} {
		if got := DisplayName(cat, agent.ParseVariant(model, cat)); got != want {
			t.Errorf("DisplayName(%q) = %q, want %q", model, got, want)
		}
	}
}

// TestCacheText checks that each reply shows how much of its prompt it read
// from cache and how much it wrote to cache, each also when it is none, so
// that a reply that did neither shows 0K for both, and that a reply that
// reported no usage at all shows none.
func TestCacheText(t *testing.T) {
	tests := []struct {
		name  string
		usage message.Usage
		want  string
	}{
		{name: "nothing"},
		{name: "cache miss", usage: message.Usage{InputTokens: 1_200, OutputTokens: 300}, want: "[cache 0K write 0K]"},
		// A prompt only written to the cache read none of it.
		{name: "cache write only", usage: message.Usage{InputTokens: 100, CacheCreationInputTokens: 900}, want: "[cache 0K write 0.9K]"},
		{name: "cache hit", usage: message.Usage{InputTokens: 9_000, CachedInputTokens: 7_700, OutputTokens: 900}, want: "[cache 7.7K write 0K]"},
		{
			name:  "cache hit and write",
			usage: message.Usage{InputTokens: 9_000, CachedInputTokens: 7_700, CacheCreationInputTokens: 1_300},
			want:  "[cache 7.7K write 1.3K]",
		},
		{name: "small cache hit", usage: message.Usage{InputTokens: 1_200, CachedInputTokens: 176}, want: "[cache 0.2K write 0K]"},
		// A reply that reports only its total read and wrote no cache.
		{name: "total only", usage: message.Usage{TotalTokens: 10}, want: "[cache 0K write 0K]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CacheText(tt.usage); got != tt.want {
				t.Errorf("CacheText(%+v) = %q, want %q", tt.usage, got, tt.want)
			}
		})
	}
}

// TestCacheCounts checks that a cache row shows the prompt tokens a request
// read from cache and wrote to cache, each also when it is none.
func TestCacheCounts(t *testing.T) {
	tests := []struct {
		name  string
		usage message.Usage
		want  string
	}{
		{name: "none", usage: message.Usage{InputTokens: 1_200}, want: "read 0K write 0K"},
		{name: "read", usage: message.Usage{InputTokens: 9_000, CachedInputTokens: 7_700}, want: "read 7.7K write 0K"},
		{name: "read and write", usage: message.Usage{CachedInputTokens: 18_000, CacheCreationInputTokens: 4_000}, want: "read 18K write 4.0K"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CacheCounts(tt.usage); got != tt.want {
				t.Errorf("CacheCounts(%+v) = %q, want %q", tt.usage, got, tt.want)
			}
		})
	}
}

// TestContextChunkColor checks that the context shows in the color it is
// given up to 70% of the window, turns warning above that, and danger above
// 90%.
func TestContextChunkColor(t *testing.T) {
	th := DefaultTheme()
	name := func(c style.Color) string {
		switch c {
		case th.TextMuted:
			return "text"
		case th.Warning:
			return "warning"
		case th.Danger:
			return "danger"
		}
		return "other"
	}
	tests := []struct {
		name    string
		context int
		want    string
	}{
		{name: "70%", context: 70, want: "text"},
		{name: "71%", context: 71, want: "warning"},
		{name: "91%", context: 91, want: "danger"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chunk := ContextChunk(SessionUsage{Context: tt.context, Window: 100}, th.TextMuted, th)
			if got := name(chunk.Fg); got != tt.want {
				t.Errorf("context color = %s, want %s", got, tt.want)
			}
		})
	}
}

// TestStatusLine checks that the slash menu's status row names the model,
// then the context, the session's token totals, its cost, and how long its
// turns took, each value after its label.
func TestStatusLine(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		usage  SessionUsage
		worked time.Duration
		want   string
	}{
		{
			name:  "session under way",
			model: "Model H Medium",
			usage: SessionUsage{Input: 120_000, Output: 8_400, CacheRead: 310_000, CacheWrite: 2_000,
				Cost: 0.4249, Context: 64_000, Window: 922_000},
			worked: 3*time.Minute + 12*time.Second,
			want:   "Model H Medium Context 64K 7% ↓ 120K ↑ 8.4K Cache 310K $0.42 Worked 3m 12s",
		},
		// Before any request every count and the cost show as none.
		{name: "new session", model: "Lab B Model D", usage: SessionUsage{Window: 200_000},
			want: "Lab B Model D Context 0K 0% ↓ 0K ↑ 0K Cache 0K $0.00 Worked 0s"},
		// With no model set, the model reads as StatusNone.
		{name: "no model", want: "None Context 0K ↓ 0K ↑ 0K Cache 0K $0.00 Worked 0s"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := StatusLine(tt.model, tt.usage, tt.worked, nil).PlainText(); got != tt.want {
				t.Errorf("status = %q\nwant     %q", got, tt.want)
			}
		})
	}
}

// usageShown returns the usage of the last EventUsageChanged in out, and
// fails the test when there is none.
func usageShown(t *testing.T, out []UIEvent) SessionUsage {
	t.Helper()
	for _, ev := range slices.Backward(out) {
		if e, ok := ev.(EventUsageChanged); ok {
			return e.Usage
		}
	}
	t.Fatalf("events %v show no usage", out)
	return SessionUsage{}
}

// TestUsageOnMessageEnd checks that a reply that ends with usage updates the
// usage shown: its tokens priced from the catalog, and the context as a
// share of the prompt window of the terminal's model. A reply without usage
// leaves it as it is.
func TestUsageOnMessageEnd(t *testing.T) {
	s, a := newTestService(t)
	s.state.Model = "provider-a/lab-a/model-c"
	model := offeringOf(t, a, "provider-a/lab-a/model-c")
	price, ok := a.Models().Catalog().Resolve("provider-a", "lab-a/model-c")
	if !ok || price.Price == nil {
		t.Fatal("the catalog lists no price for provider-a/lab-a/model-c")
	}
	reply := message.Content{ID: "a1", Role: message.RoleAgent, Provider: providerA, Model: "lab-a/model-c"}
	s.ApplyAgentEvent(app.EventMessageStarted{Message: app.Message{Content: reply}})

	stopped := s.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{Content: reply}, Stopped: true})
	if slices.ContainsFunc(stopped, func(ev UIEvent) bool { _, ok := ev.(EventUsageChanged); return ok }) {
		t.Error("a reply without usage changed the usage shown")
	}

	reply.Parts = []message.ContentPart{message.UsagePart{ID: "a1-usage", Usage: message.Usage{
		InputTokens: 10_000, CachedInputTokens: 8_000, OutputTokens: 500, TotalTokens: 10_500,
	}}}
	got := usageShown(t, s.ApplyAgentEvent(app.EventMessageEnded{Message: app.Message{Content: reply}}))
	want := SessionUsage{
		Input: 2_000, Output: 500, CacheRead: 8_000,
		Cost:    price.Price.At(10_000).Charge(2_000, 8_000, 0, 500),
		Context: 10_500, Window: model.Limit.PromptWindow(),
	}
	if got != want {
		t.Errorf("usage = %+v\nwant    %+v", got, want)
	}
	if s.state.Usage != got {
		t.Errorf("state usage = %+v, want %+v", s.state.Usage, got)
	}

	// Another model changes the window, and keeps the totals.
	if got := usageShown(t, s.SelectModel("provider-a/model")); got.Window != 0 || got.Input != want.Input {
		t.Errorf("usage after switching to a model the catalog lacks = %+v, want no window and the same totals", got)
	}
}

func TestFormatTokens(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "0K"},
		// No count but zero reads as none.
		{1, "0.1K"},
		{99, "0.1K"},
		{176, "0.2K"},
		{300, "0.3K"},
		{999, "1.0K"},
		{1_000, "1.0K"},
		{1_100, "1.1K"},
		{9_949, "9.9K"},
		{9_950, "10K"},
		{27_400, "27K"},
		{271_600, "272K"},
		{999_499, "999K"},
		{999_500, "1.0M"},
		{1_250_000, "1.2M"},
		{9_950_000, "10M"},
	}
	for _, tt := range tests {
		if got := FormatTokens(tt.n); got != tt.want {
			t.Errorf("FormatTokens(%d) = %q, want %q", tt.n, got, tt.want)
		}
	}
}
