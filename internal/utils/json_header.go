package utils

import (
	"fmt"
	"strings"
	"unicode/utf16"

	"github.com/bytedance/sonic"
)

// MarshalJSONHeader 将 JSON 编码为纯 ASCII，避免浏览器将 HTTP 响应头中的 UTF-8 当作 Latin-1
func MarshalJSONHeader(value any) (string, error) {
	encoded, err := sonic.MarshalString(value)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.Grow(len(encoded))
	for _, r := range encoded {
		switch {
		case r < 128:
			b.WriteRune(r)
		case r <= 0xffff:
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", hi, lo)
		}
	}
	return b.String(), nil
}
