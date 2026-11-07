// Package post sends the JSON requests of the providers, reporting failures
// as the errors of package agent.
package post

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"

	"github.com/unaquatic/flow/agent"
)

// JSON posts body as JSON to url with header, for the provider named name,
// and returns the response, whose body the caller closes. A request that
// cannot be sent fails with a *agent.RequestError, and a response other
// than 200 OK with a *agent.ResponseError, whose body JSON reads and
// closes.
func JSON(ctx context.Context, client *http.Client, name, url string, header http.Header, body any) (*http.Response, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, &agent.RequestError{Err: err}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, &agent.RequestError{Err: err}
	}
	maps.Copy(req.Header, header)
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, &agent.RequestError{Err: err}
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return nil, &agent.ResponseError{
		Provider:        name,
		StatusCode:      resp.StatusCode,
		Message:         errorMessage(raw),
		ResponseHeaders: resp.Header,
		ResponseBody:    string(raw),
	}
}

// errorMessage returns the error.message field of an error response body,
// where Anthropic, OpenAI and Google put it, or the message field, where
// some others do, or else the whole body.
func errorMessage(body []byte) string {
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &parsed) == nil && cmp.Or(parsed.Error.Message, parsed.Message) != "" {
		return cmp.Or(parsed.Error.Message, parsed.Message)
	}
	return string(body)
}
