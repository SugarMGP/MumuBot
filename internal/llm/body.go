package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/bytedance/sonic"
)

// ParseBodyOverride 校验覆盖对象；RawMessage 避免大整数经过浮点中转
func ParseBodyOverride(text string) (map[string]json.RawMessage, error) {
	if strings.TrimSpace(text) == "" {
		return map[string]json.RawMessage{}, nil
	}
	var fields map[string]json.RawMessage
	if err := sonic.UnmarshalString(text, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("body override must be a JSON object")
	}
	if _, exists := fields["stream"]; exists {
		return nil, fmt.Errorf("body override cannot set stream")
	}
	return fields, nil
}

type bodyOverrideTransport struct {
	base   http.RoundTripper
	fields map[string]json.RawMessage
}

func (t *bodyOverrideTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	defer request.Body.Close()
	raw, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	var body map[string]json.RawMessage
	if err := sonic.Unmarshal(raw, &body); err != nil {
		return nil, fmt.Errorf("decode model request: %w", err)
	}
	if err := mergeBodyObject(body, t.fields); err != nil {
		return nil, err
	}
	raw, err = sonic.Marshal(body)
	if err != nil {
		return nil, err
	}
	modified := request.Clone(request.Context())
	modified.Body = io.NopCloser(bytes.NewReader(raw))
	modified.ContentLength = int64(len(raw))
	modified.GetBody = func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(raw)), nil }
	return t.base.RoundTrip(modified)
}

func mergeBodyObject(body, override map[string]json.RawMessage) error {
	for key, value := range override {
		original := bytes.TrimSpace(body[key])
		replacement := bytes.TrimSpace(value)
		if len(original) > 0 && original[0] == '{' && len(replacement) > 0 && replacement[0] == '{' {
			var existing, incoming map[string]json.RawMessage
			if err := sonic.Unmarshal(original, &existing); err != nil {
				return err
			}
			if err := sonic.Unmarshal(replacement, &incoming); err != nil {
				return err
			}
			if err := mergeBodyObject(existing, incoming); err != nil {
				return err
			}
			merged, err := sonic.Marshal(existing)
			if err != nil {
				return err
			}
			body[key] = merged
		} else {
			body[key] = value
		}
	}
	return nil
}
