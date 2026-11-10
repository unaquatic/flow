package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"iter"
	"log/slog"
	"maps"
	"slices"

	"github.com/unaquatic/flow/agent/message"
)

// BlockKind identifies text or reasoning content.
type BlockKind int

const (
	// BlockText identifies agent text.
	BlockText BlockKind = iota
	// BlockReasoning identifies model reasoning.
	BlockReasoning
)

// Part returns a content part for this block kind.
func (k BlockKind) Part(id message.PartID, text string) message.ContentPart {
	if k == BlockReasoning {
		return message.ReasoningPart{ID: id, Text: text}
	}
	return message.TextPart{ID: id, Text: text}
}

// BlockKey identifies a streamed text or reasoning block of a response.
type BlockKey struct {
	// Kind selects text or reasoning output.
	Kind BlockKind
	// Block identifies the provider content block.
	Block Block
}

// TurnState accumulates one agent turn and emits its events.
type TurnState struct {
	// Emit receives events in emission order.
	Emit func(Event)

	// MessageID identifies the agent message.
	MessageID message.ID
	// Variant identifies the model producing the message, as it runs.
	Variant Variant

	// Parts is the agent message under construction.
	Parts []message.ContentPart

	// Usage is the token counts the provider reported last, until EndBlocks
	// records them as the message's usage part.
	Usage message.Usage
	// Reason describes why generation stopped.
	Reason StopReason

	// Blocks maps each streamed text or reasoning block to the index of its
	// part in Parts.
	Blocks map[BlockKey]int
	// Ended holds the call IDs of the tool calls whose input the provider
	// ended, and so is complete.
	Ended map[string]bool
}

// NewTurnState returns an empty turn state for message messageID, which model
// variant v produces, and whose events go to emit.
func NewTurnState(messageID message.ID, v Variant, emit func(Event)) *TurnState {
	return &TurnState{
		Emit:      emit,
		MessageID: messageID,
		Variant:   v,
		Blocks:    make(map[BlockKey]int),
		Ended:     make(map[string]bool),
	}
}

// Message returns the message as it is so far, as events carry it.
func (s *TurnState) Message() message.Content {
	return message.Content{
		ID:   s.MessageID,
		Role: message.RoleAgent,
		// Later changes replace parts in s.Parts.
		Parts:    slices.Clone(s.Parts),
		Provider: s.Variant.Provider,
		Model:    s.Variant.Ref(),
	}
}

// StartPart appends part to the message and emits PartStarted.
func (s *TurnState) StartPart(part message.ContentPart) {
	s.Parts = append(s.Parts, part)
	s.Emit(PartStarted{PartChange{MessageID: s.MessageID, Part: part}})
}

// UpdatePart replaces the part at slot and emits PartUpdated.
func (s *TurnState) UpdatePart(slot int, part message.ContentPart) {
	s.Parts[slot] = part
	s.Emit(PartUpdated{PartChange{MessageID: s.MessageID, Part: part}})
}

// EndPart replaces the part at slot with its final form and emits PartEnded.
func (s *TurnState) EndPart(slot int, part message.ContentPart) {
	s.Parts[slot] = part
	s.Emit(PartEnded{PartChange{MessageID: s.MessageID, Part: part}})
}

// Sink returns the PartSink of a hook adding parts to the end of the
// message: Start starts a part, as StartPart does, and End ends the part
// with the same ID, as EndPart does.
func (s *TurnState) Sink() PartSink {
	return PartSink{
		Start: s.StartPart,
		End: func(part message.ContentPart) {
			slot := slices.IndexFunc(s.Parts, func(p message.ContentPart) bool { return p.PartID() == part.PartID() })
			if slot >= 0 {
				s.EndPart(slot, part)
			}
		},
	}
}

// Consume drains a provider stream into the turn, stopping at the first
// stream error or when ctx ends.
func (s *TurnState) Consume(ctx context.Context, stream StreamResponse) error {
	for part, err := range stream {
		if err == nil {
			err = ctx.Err()
		}
		if err != nil {
			return err
		}
		s.Handle(part)
	}
	return nil
}

// Handle folds one provider stream part into the turn.
func (s *TurnState) Handle(part StreamPart) {
	switch p := part.(type) {
	case TextDelta:
		s.AppendText(BlockText, p.Block, p.Text)
	case ReasoningDelta:
		s.AppendText(BlockReasoning, p.Block, p.Text)
	case ReasoningSignature:
		s.SignReasoning(p)
	case ToolUseStart:
		s.OpenTool(p.ID, p.Name)
	case ToolUseDelta:
		s.GrowTool(p.ID, p.JSON)
	case ToolUseEnd:
		s.EndTool(p.ID, p.Input)
	case Usage:
		s.Usage = UsageFrom(p)
	case Stop:
		s.Reason = p.Reason
	}
}

// AppendText adds chunk to a block's part, starting the part with the block's
// first chunk and updating it with each later one.
func (s *TurnState) AppendText(kind BlockKind, block Block, chunk string) {
	if chunk == "" {
		return
	}
	key := BlockKey{Kind: kind, Block: block}
	slot, ok := s.Blocks[key]
	if !ok {
		s.Blocks[key] = len(s.Parts)
		s.StartPart(kind.Part(message.NewPartID(), chunk))
		return
	}
	switch p := s.Parts[slot].(type) {
	case message.TextPart:
		p.Text += chunk
		s.UpdatePart(slot, p)
	case message.ReasoningPart:
		p.Text += chunk
		s.UpdatePart(slot, p)
	}
}

// SignReasoning sets the signature of a reasoning block's part, and whether
// its reasoning is redacted, as sig gives them, starting the part if the
// block streamed no text.
func (s *TurnState) SignReasoning(sig ReasoningSignature) {
	key := BlockKey{Kind: BlockReasoning, Block: sig.Block}
	slot, ok := s.Blocks[key]
	if !ok {
		s.Blocks[key] = len(s.Parts)
		s.StartPart(message.ReasoningPart{ID: message.NewPartID(), Signature: sig.Signature, Redacted: sig.Redacted})
		return
	}
	if p, ok := s.Parts[slot].(message.ReasoningPart); ok {
		p.Signature, p.Redacted = sig.Signature, sig.Redacted
		s.UpdatePart(slot, p)
	}
}

// EndBlocks ends every text and reasoning part still streaming, in order,
// and then records the usage the provider reported. The provider does not
// mark where a block ends, and may report usage more than once, each report
// replacing the one before, so they all end once the response stops
// streaming.
func (s *TurnState) EndBlocks() {
	for _, slot := range slices.Sorted(maps.Values(s.Blocks)) {
		s.EndPart(slot, s.Parts[slot])
	}
	clear(s.Blocks)
	s.EndUsage()
}

// EndUsage records the usage the provider reported last as a usage part,
// which is final as it starts, and clears it, so that a message records its
// usage once. It adds no part when the provider reported none.
func (s *TurnState) EndUsage() {
	if s.Usage.IsZero() {
		return
	}
	part := message.UsagePart{ID: message.NewPartID(), Usage: s.Usage}
	s.Usage = message.Usage{}
	s.StartPart(part)
	s.EndPart(len(s.Parts)-1, part)
}

// OpenTool starts a pending tool part.
func (s *TurnState) OpenTool(callID, name string) {
	s.StartPart(message.ToolPart{
		ID:     message.NewPartID(),
		CallID: callID,
		Name:   name,
		State:  message.ToolStatePending{},
	})
}

// GrowTool appends fragment to the input of pending tool call callID and
// updates its part.
func (s *TurnState) GrowTool(callID, fragment string) {
	slot, tp, ok := s.PendingTool(callID)
	if !ok {
		slog.Warn("drop tool input of unknown call", "call_id", callID)
		return
	}
	input := string(tp.Input()) + fragment
	s.UpdatePart(slot, tp.WithState(message.ToolStatePending{Input: json.RawMessage(input)}))
}

// EndTool records input as the complete input of pending tool call callID,
// which makes the call ready to run, and updates its part if that changes
// the input.
func (s *TurnState) EndTool(callID string, input json.RawMessage) {
	slot, tp, ok := s.PendingTool(callID)
	if !ok {
		slog.Warn("drop tool input of unknown call", "call_id", callID)
		return
	}
	s.Ended[callID] = true
	if bytes.Equal(tp.Input(), input) {
		return
	}
	s.UpdatePart(slot, tp.WithState(message.ToolStatePending{Input: slices.Clone(input)}))
}

// PendingTool returns pending tool call callID's part and its index in
// Parts, and reports whether there is one.
func (s *TurnState) PendingTool(callID string) (int, message.ToolPart, bool) {
	for slot, tp := range s.PendingTools() {
		if tp.CallID == callID {
			return slot, tp, true
		}
	}
	return 0, message.ToolPart{}, false
}

// Interrupt ends each tool call the turn left unfinished, pending or running,
// as interrupted.
func (s *TurnState) Interrupt() {
	for slot, part := range s.Parts {
		tp, ok := part.(message.ToolPart)
		if !ok {
			continue
		}
		switch st := tp.State.(type) {
		case message.ToolStatePending:
			s.EndPart(slot, tp.WithState(message.ToolStateError{Input: st.Input, Error: message.InterruptedError}))
		case message.ToolStateRunning:
			s.EndPart(slot, tp.WithState(message.ToolStateError{Input: st.Input, Error: message.InterruptedError, StartedAt: st.StartedAt}))
		}
	}
}

// PendingTools yields each tool part still pending and its index in Parts.
func (s *TurnState) PendingTools() iter.Seq2[int, message.ToolPart] {
	return func(yield func(int, message.ToolPart) bool) {
		for slot, part := range s.Parts {
			tp, ok := part.(message.ToolPart)
			if !ok {
				continue
			}
			if _, pending := tp.State.(message.ToolStatePending); pending && !yield(slot, tp) {
				return
			}
		}
	}
}

// UsageFrom converts provider usage and derives a missing total.
func UsageFrom(p Usage) message.Usage {
	u := message.Usage{
		InputTokens:              p.InputTokens,
		CachedInputTokens:        p.CachedInputTokens,
		CacheCreationInputTokens: p.CacheCreationInputTokens,
		OutputTokens:             p.OutputTokens,
		ReasoningOutputTokens:    p.ReasoningOutputTokens,
		TotalTokens:              p.TotalTokens,
	}
	if u.TotalTokens == 0 {
		u.TotalTokens = u.InputTokens + u.CacheCreationInputTokens + u.OutputTokens
	}
	return u
}
