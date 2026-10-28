package proxy

import "encoding/json"

// InjectUsage adds stream_options.include_usage for streaming chat requests.
// It returns the original bytes when the body is not JSON, is not streaming,
// or already carries stream_options.
func InjectUsage(body []byte) ([]byte, bool) {
	var req map[string]any
	if err := json.Unmarshal(body, &req); err != nil || req == nil {
		return body, false
	}
	stream, _ := req["stream"].(bool)
	if !stream {
		return body, false
	}
	if _, exists := req["stream_options"]; exists {
		return body, false
	}
	req["stream_options"] = map[string]any{"include_usage": true}
	out, err := json.Marshal(req)
	if err != nil {
		return body, false
	}
	return out, true
}

func ParseModel(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Model
}

func ParseResponseModel(body []byte) string {
	var v struct {
		Model    string `json:"model"`
		Response *struct {
			Model string `json:"model"`
		} `json:"response"`
	}
	_ = json.Unmarshal(body, &v)
	if v.Response != nil && v.Response.Model != "" {
		return v.Response.Model
	}
	return v.Model
}
