package app

import (
	"context"

	"github.com/unaquatic/flow/agent"
)

// Generate sends req to model, a variant in the form Resolve reads, or
// to the default model when model is empty, outside any session, and returns
// the whole response and the variant it went to, as Variant.String writes it.
func (m Models) Generate(ctx context.Context, model string, req agent.Request) (*agent.Response, string, error) {
	v, err := m.Resolve(model)
	if err != nil {
		return nil, "", err
	}
	resp, err := m.app.providers.Generate(ctx, v, req)
	return resp, v.String(), err
}

// GenerateStream is like Generate, but returns the response as a stream.
func (m Models) GenerateStream(ctx context.Context, model string, req agent.Request) (agent.StreamResponse, string, error) {
	v, err := m.Resolve(model)
	if err != nil {
		return nil, "", err
	}
	stream, err := m.app.providers.Stream(ctx, v, req)
	return stream, v.String(), err
}
