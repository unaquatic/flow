package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/unaquatic/catalog/gateway"
	"github.com/unaquatic/flow/agent"
	"github.com/unaquatic/flow/agent/message"
	"github.com/unaquatic/flow/app"
	"github.com/unaquatic/flow/sse"
)

// handleModelList answers with the catalog models of each signed-in
// provider, or of the one the provider query names.
func (s *Server) handleModelList(w http.ResponseWriter, r *http.Request) {
	listings, err := s.app.Models().Listings(r.Context(), agent.ProviderID(r.URL.Query().Get("provider")))
	if err != nil {
		respondModelErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, listings)
}

// handleModelRefresh downloads the model catalog, and answers with the
// listings of GET /model taken from it. A failure of the catalog source is a
// bad gateway; one storing the catalog is the server's own.
func (s *Server) handleModelRefresh(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Models().Refresh(r.Context()); err != nil {
		respondError(w, modelErrStatus(err), "refresh the model catalog: "+err.Error())
		return
	}
	s.handleModelList(w, r)
}

// handleModelGet answers with the catalog entry of one model.
func (s *Server) handleModelGet(w http.ResponseWriter, r *http.Request) {
	m, err := s.app.Models().Listing(agent.ProviderID(r.PathValue("provider")), r.PathValue("id"))
	if err != nil {
		respondModelErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, m)
}

// handleGenerate answers one model request outside any session.
func (s *Server) handleGenerate(w http.ResponseWriter, r *http.Request) {
	var body GenerateRequest
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	req, err := body.Request()
	if err != nil {
		respondError(w, http.StatusBadRequest, err.Error())
		return
	}
	if body.Stream {
		s.streamGenerate(w, r, body.Model, req)
		return
	}
	resp, model, err := s.app.Models().Generate(r.Context(), body.Model, req)
	if err != nil {
		respondModelErr(w, err)
		return
	}
	respondJSON(w, http.StatusOK, generateResponse(model, resp))
}

// streamGenerate answers req with server-sent events: a delta for each chunk
// of text or reasoning as it arrives, then the whole reply, or the failure
// that ended it. A request that cannot start is answered with its error
// status instead.
func (s *Server) streamGenerate(w http.ResponseWriter, r *http.Request, model string, req agent.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		respondError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	stream, name, err := s.app.Models().GenerateStream(r.Context(), model, req)
	if err != nil {
		respondModelErr(w, err)
		return
	}
	sse.Start(w)
	flusher.Flush()

	// Send each delta on, and collect the parts for the whole reply.
	forwarded := agent.StreamResponse(func(yield func(agent.StreamPart, error) bool) {
		for part, err := range stream {
			if err == nil {
				if writeDelta(w, part) != nil {
					return
				}
				flusher.Flush()
			}
			if !yield(part, err) {
				return
			}
		}
	})
	resp, err := forwarded.Collect()
	if err != nil {
		writeEvent(w, EventGenerateFailed, GenerateFailed{Error: err.Error()})
	} else {
		writeEvent(w, EventGenerateEnded, generateResponse(name, resp))
	}
	flusher.Flush()
}

// writeDelta writes part as a delta event, if it is text or reasoning.
func writeDelta(w io.Writer, part agent.StreamPart) error {
	switch p := part.(type) {
	case agent.TextDelta:
		return writeEvent(w, EventGenerateTextDelta, GenerateDelta{Block: p.Block, Text: p.Text})
	case agent.ReasoningDelta:
		return writeEvent(w, EventGenerateReasoningDelta, GenerateDelta{Block: p.Block, Text: p.Text})
	}
	return nil
}

// generateResponse returns the wire form of resp, a reply of model.
func generateResponse(model string, resp *agent.Response) GenerateResponse {
	return GenerateResponse{
		Model:      model,
		Text:       resp.Message.TextContent(),
		Message:    resp.Message,
		StopReason: resp.StopReason,
		Usage:      agent.UsageFrom(resp.Usage),
	}
}

// respondModelErr reports err, an error of a model or catalog request, with
// the status modelErrStatus maps it to.
func respondModelErr(w http.ResponseWriter, err error) {
	respondError(w, modelErrStatus(err), err.Error())
}

// modelErrStatus maps the errors of model and catalog requests onto HTTP
// status codes. A provider or the catalog source that failed or answered
// with an error is a bad gateway.
func modelErrStatus(err error) int {
	switch {
	case errors.Is(err, agent.ErrProviderNotRegistered),
		errors.Is(err, agent.ErrModelNotFound),
		errors.Is(err, agent.ErrVariantNotFound):
		return http.StatusNotFound
	case errors.Is(err, app.ErrModelFormat),
		errors.Is(err, agent.ErrFormatNotSupported),
		errors.Is(err, agent.ErrMissingVariable):
		return http.StatusBadRequest
	case errors.Is(err, agent.ErrNotAuthenticated):
		return http.StatusUnauthorized
	case errors.Is(err, gateway.ErrSource):
		return http.StatusBadGateway
	}
	if _, ok := errors.AsType[*agent.ResponseError](err); ok {
		return http.StatusBadGateway
	}
	if _, ok := errors.AsType[*agent.RequestError](err); ok {
		return http.StatusBadGateway
	}
	if _, ok := errors.AsType[*agent.ResponseParseError](err); ok {
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// GenerateRequest is the body of POST /generate: one model request outside
// any session.
type GenerateRequest struct {
	// Model is the model to ask, in the format of SendRequest.Model. Empty
	// selects the default model, as GET /config reports it.
	Model string `json:"model,omitempty"`
	// Messages is the conversation to answer, oldest first.
	Messages []GenerateMessage `json:"messages"`
	// MaxTokens limits the output tokens of the reply. Zero takes the
	// model's limit from the catalog.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Stream answers with server-sent events instead of one JSON body:
	// generate.text.delta and generate.reasoning.delta as the reply
	// streams, then generate.ended with what the JSON body would hold, or
	// generate.failed.
	Stream bool `json:"stream,omitempty"`
}

// GenerateMessage is one text message of a GenerateRequest.
type GenerateMessage struct {
	// Role is "system", "user", or "agent".
	Role string `json:"role"`
	// Content is the message text.
	Content string `json:"content"`
}

// GenerateResponse is the answer of POST /generate, and the data of its
// generate.ended event.
type GenerateResponse struct {
	// Model is the model asked, in the format of SendRequest.Model, at the
	// reasoning effort it ran at.
	Model string `json:"model"`
	// Text is the text of the reply.
	Text string `json:"text"`
	// Message is the whole reply, with its parts.
	Message agent.Message `json:"message"`
	// StopReason is why the model stopped.
	StopReason agent.StopReason `json:"stop_reason"`
	// Usage contains the token counts of the request.
	Usage message.Usage `json:"usage"`
}

// GenerateDelta is the data of a generate.text.delta or
// generate.reasoning.delta event.
type GenerateDelta struct {
	// Block identifies the block of the reply the text belongs to.
	Block agent.Block `json:"block"`
	// Text is the text added to the block.
	Text string `json:"text"`
}

// GenerateFailed is the data of a generate.failed event.
type GenerateFailed struct {
	// Error describes the failure.
	Error string `json:"error"`
}

// Event types of a streamed POST /generate.
const (
	// EventGenerateTextDelta carries text of the reply as it streams.
	EventGenerateTextDelta = "generate.text.delta"
	// EventGenerateReasoningDelta carries reasoning as it streams.
	EventGenerateReasoningDelta = "generate.reasoning.delta"
	// EventGenerateEnded carries the whole reply, and ends the stream.
	EventGenerateEnded = "generate.ended"
	// EventGenerateFailed carries the failure that ends the stream.
	EventGenerateFailed = "generate.failed"
)

// generateRoles maps the roles a GenerateMessage may name to message roles.
var generateRoles = map[string]message.Role{
	"system": message.RoleSystem,
	"user":   message.RoleUser,
	"agent":  message.RoleAgent,
}

// Request returns the provider request body asks for, or an error saying
// what is invalid.
func (body GenerateRequest) Request() (agent.Request, error) {
	if len(body.Messages) == 0 {
		return agent.Request{}, errors.New("messages must not be empty")
	}
	if body.MaxTokens < 0 {
		return agent.Request{}, errors.New("max_tokens must not be negative")
	}
	msgs := make([]agent.Message, 0, len(body.Messages))
	for i, m := range body.Messages {
		role, ok := generateRoles[m.Role]
		if !ok {
			return agent.Request{}, fmt.Errorf("messages[%d].role must be system, user or agent", i)
		}
		msgs = append(msgs, agent.NewTextMessage(role, m.Content))
	}
	return agent.Request{Messages: msgs, MaxTokens: body.MaxTokens}, nil
}
