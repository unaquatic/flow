package agent

import (
	"strings"
	"testing"
)

// TestResponseErrorError checks that a response error shows the provider's
// raw body on one line, compacted when it is JSON and cut short when it is
// long, and its message when it has no body.
func TestResponseErrorError(t *testing.T) {
	long := strings.Repeat("é", MaxErrorBody)
	for _, tt := range []struct {
		name string
		err  *ResponseError
		want string
	}{
		{
			name: "no body",
			err:  &ResponseError{Provider: "anthropic", StatusCode: 0, Message: "Overloaded"},
			want: "anthropic response error (0): Overloaded",
		},
		{
			name: "json body",
			err: &ResponseError{Provider: "anthropic", StatusCode: 429, Message: "rate_limit_error",
				ResponseBody: "{\n  \"type\": \"error\",\n  \"error\": {\"type\": \"rate_limit_error\", \"message\": \"Error\"},\n  \"request_id\": \"req_1\"\n}\n"},
			want: `anthropic response error (429): {"type":"error","error":{"type":"rate_limit_error","message":"Error"},"request_id":"req_1"}`,
		},
		{
			name: "html body",
			err: &ResponseError{Provider: "openai", StatusCode: 502, Message: "Bad Gateway",
				ResponseBody: "<html>\n  <body>\n\t<h1>502 Bad Gateway</h1>\n  </body>\n</html>\n"},
			want: "openai response error (502): <html> <body> <h1>502 Bad Gateway</h1> </body> </html>",
		},
		{
			name: "long body",
			err:  &ResponseError{Provider: "openai", StatusCode: 500, ResponseBody: long},
			want: "openai response error (500): " + long[:MaxErrorBody] + "…",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.err.Error(); got != tt.want {
				t.Errorf("Error() = %q, want %q", got, tt.want)
			}
		})
	}
}
