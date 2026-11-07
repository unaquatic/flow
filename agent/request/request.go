// Package request shapes the requests providers send: it merges the body
// fields and headers that a catalog variant sets over the provider's own.
package request

import "net/http"

// MergeBody sets the fields of src in dst, which it makes when nil, and
// returns dst. A field that holds an object in both is merged field by
// field; any other field of src replaces the field of dst. The objects it
// takes from src are copied, so dst shares none of them with src.
func MergeBody(dst, src map[string]any) map[string]any {
	if dst == nil {
		dst = make(map[string]any, len(src))
	}
	for k, v := range src {
		if obj, ok := v.(map[string]any); ok {
			into, _ := dst[k].(map[string]any)
			dst[k] = MergeBody(into, obj)
			continue
		}
		dst[k] = v
	}
	return dst
}

// AddHeaders adds the headers of extra to h. A header that h holds already
// takes the value after its own, comma-separated, as list headers such as
// anthropic-beta take them.
func AddHeaders(h http.Header, extra map[string]string) {
	for k, v := range extra {
		if cur := h.Get(k); cur != "" {
			v = cur + "," + v
		}
		h.Set(k, v)
	}
}
