package tools

// ContextClassification 保存回复前上下文分类结果。
type ContextClassification struct {
	Intent     string `json:"intent" jsonschema:"enum=轻松起哄,enum=认同接话,enum=询问推进,enum=安抚缓和,description=当前聊天更适合参考的发言方向"`
	Tone       string `json:"tone" jsonschema:"enum=直接,enum=轻松,enum=夸张,enum=克制,description=当前聊天更适合参考的语气标签"`
	TopicQuery string `json:"topic_query" jsonschema:"description=用于检索历史话题和长期记忆的短查询；低信息量或不需要检索时留空"`
}
