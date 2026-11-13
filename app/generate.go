package app

import (
	"context"

	"github.com/unaquatic/flow/agent"
)

// Generate sends req to model, a variant in the form Resolve reads, or
// to the default model when model is empty, outside any session, and returns
// the whole response and the variant it went to.
func (m Models) Generate(ctx context.Context, model string, req agent.Request) (*agent.Response, agent.Variant, error) {
	v, err := m.Resolve(model)
	if err != nil {
		return nil, agent.Variant{}, err
	}
	resp, err := m.app.providers.Generate(ctx, v, req)
	return resp, v, err
}

// GenerateStream is like Generate, but returns the response as a stream.
func (m Models) GenerateStream(ctx context.Context, model string, req agent.Request) (agent.StreamResponse, agent.Variant, error) {
	v, err := m.Resolve(model)
	if err != nil {
		return nil, agent.Variant{}, err
	}
	stream, err := m.app.providers.Stream(ctx, v, req)
	return stream, v, err
}
