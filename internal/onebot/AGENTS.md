# AGENTS

本目录负责 OneBot 11 协议接入：使用 napcat-sdk 管理 WebSocket，在内部 Adapter 中解析事件、缓存成员信息、发送群消息和调用 OneBot API。

## 入口与职责

- `client.go`：SDK 连接与自动重连、每群有界保序 worker、原始 notice 分发和消息回调。
- `client_parse.go`：消息段解析、基础展示文本、回复 ID、图片和转发内容解析。
- `client_api.go`：发送消息、戳一戳、群信息、群成员、公告、精华和转发相关 API。
- `client_types.go`：消息段、群消息和动态 API 解析结果类型。

## 原文与展示边界

- `Content` 只表示原始文本，主要来自 text 消息段。
- `FinalContent` 是展示给 bot 的聊天上下文，可以包含 @、语音、文件、卡片、合并转发等提示文本。
- @ 成员、@ 全体成员、语音、文件、卡片、合并转发等非文本段不要写入原始文本。
- @ 全体成员使用虚拟 QQ 号 `AtAllUserID = -1` 表示，不新增额外布尔字段。

## 解析规则

- 消息段解析应优先填结构化列表，例如 `AtList`、图片、文件、卡片、转发；需要判断是否存在时直接看列表是否为空。
- 不要在解析层猜测未知字段名；OneBot 字段不确定时先查协议或保留解析失败日志。
- 解析失败不应制造文本占位污染原始内容；最多进入展示上下文。
- JSON 解析和序列化复用 Sonic 并保留整数精度；动态 API 返回体必须校验真实结构。

## 修改注意

- 修改消息类型或解析结果字段时，检查 `internal/topic`、`internal/agent`、`internal/learning` 是否把该字段当语义输入。
- SDK event loop 只做事件分流：`meta_event`、`notice`、`request` 在 Adapter 入口同步处理，只有群消息进入每群有界保序 worker。
- Adapter 解析阶段不执行远程 API；消息回调先落库，再在群 worker 的业务回调中完成标已读、回复补充、转发展开和视觉识别等慢路径。
- Adapter 继续负责自动重连、主动关闭、notice 原始解析、禁言状态、成员缓存和动态返回体校验，不把这些职责下沉到业务层。
- 每群队列使用阻塞背压，不静默丢弃；关闭顺序必须先停止 SDK 事件流，再关闭群队列并等待 worker 排空。
- 当前不增加 PostgreSQL 事件 inbox，也不承诺上游断线后的 exactly-once；没有明确需求时不要为此新增表、配置或中间件。
- API 调用错误要保留真实失败，不要把失败包装成“已发送”。
