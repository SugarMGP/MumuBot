package llm

import (
	"context"
	"fmt"
	"strings"

	jsonrepair "github.com/RealAlexandreAI/json-repair"
	"github.com/bytedance/sonic"
	openaimodel "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"
	toolutils "github.com/cloudwego/eino/components/tool/utils"
	"github.com/cloudwego/eino/schema"
)

// GenerateStructuredJSONObject 使用 OpenAI 兼容模型的 json_object 输出模式，
// 并把目标结构体对应的 JSON Schema 作为 system 提示词约束输出，
// 调用方传入的业务 prompt 直接作为 user 提示词，最后自动反序列化为 T。
func GenerateStructuredJSONObject[T any](ctx context.Context, chatModel model.BaseChatModel, userPrompt string, opts ...model.Option) (T, error) {
	var zero T

	if chatModel == nil {
		return zero, fmt.Errorf("chatModel 不能为空")
	}
	if strings.TrimSpace(userPrompt) == "" {
		return zero, fmt.Errorf("userPrompt 不能为空")
	}

	paramsOneOf, err := toolutils.GoStruct2ParamsOneOf[T]()
	if err != nil {
		return zero, fmt.Errorf("生成结构化输出 schema 失败: %w", err)
	}
	jsonSchema, err := paramsOneOf.ToJSONSchema()
	if err != nil {
		return zero, fmt.Errorf("转换结构化输出 schema 失败: %w", err)
	}
	schemaJSON, err := sonic.MarshalString(jsonSchema)
	if err != nil {
		return zero, fmt.Errorf("序列化结构化输出 schema 失败: %w", err)
	}

	systemPrompt := strings.TrimSpace(`请你按照要求输出一个 JSON object，不要输出任何额外文本、解释、Markdown、代码块或前后缀。
输出字段、字段名、required 约束、枚举值、数组/对象层级都必须严格符合下面的 JSON Schema，不要补充 schema 未声明的字段。

JSON Schema:
` + schemaJSON)

	input := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(userPrompt),
	}
	callOpts := append([]model.Option{
		openaimodel.WithRequestPayloadModifier(func(_ context.Context, _ []*schema.Message, rawBody []byte) ([]byte, error) {
			var payload map[string]any
			if err := sonic.Unmarshal(rawBody, &payload); err != nil {
				return nil, fmt.Errorf("解析模型请求体失败: %w", err)
			}
			payload["response_format"] = map[string]any{
				"type": "json_object",
			}
			body, err := sonic.Marshal(payload)
			if err != nil {
				return nil, fmt.Errorf("序列化模型请求体失败: %w", err)
			}
			return body, nil
		}),
	}, opts...)

	resp, err := chatModel.Generate(ctx, input, callOpts...)
	if err != nil {
		return zero, err
	}
	if resp == nil {
		return zero, fmt.Errorf("结构化输出响应为空")
	}

	parsed, err := parseStructuredJSON[T](resp.Content)
	if err != nil {
		content := strings.TrimSpace(resp.Content)
		contentRunes := []rune(content)
		if len(contentRunes) > 240 {
			content = string(contentRunes[:240]) + "..."
		}
		return zero, fmt.Errorf("解析结构化 JSON 失败: %w; content=%q", err, content)
	}

	return parsed, nil
}

func parseStructuredJSON[T any](content string) (T, error) {
	var parsed T
	if !sonic.ValidString(content) {
		var err error
		content, err = jsonrepair.RepairJSON(content)
		if err != nil {
			return parsed, fmt.Errorf("修复结构化 JSON 失败: %w", err)
		}
	}
	if err := sonic.UnmarshalString(content, &parsed); err != nil {
		return parsed, fmt.Errorf("反序列化结构化 JSON 失败: %w", err)
	}
	return parsed, nil
}
