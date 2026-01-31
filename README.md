# 阿沐 - 赛博QQ群友

一个"以假乱真"的长期存在型QQ群友Agent，不是传统指令机器人。

## 特性

- **真实群友模拟**：阿沐是一个常驻QQ群的普通群友，有稳定的人格、情绪与语言风格
- **自主决策**：会观察群聊，自己决定是否发言、何时发言、发什么内容
- **长期记忆**：具备记忆系统，能记住群内事实、成员画像、自身经历
- **智能筛选**：不是所有内容都存储，由模型自己判断是否值得记忆
- **MCP工具调用**：通过Tool Calling让模型"自己决定做什么"

## 技术栈

- **语言**: Golang
- **Web框架**: Gin
- **ORM**: Gorm
- **Agent框架**: Eino（字节跳动）
- **数据库**: SQLite + 向量检索
- **QQ接入**: NapCat + OneBot v11

## 项目结构

```
amu_bot/
├── cmd/
│   └── amu/
│       └── main.go          # 主程序入口
├── config/
│   └── config.yaml          # 配置文件
├── internal/
│   ├── agent/
│   │   └── agent.go         # Agent核心（Observer + Think + Act）
│   ├── config/
│   │   └── config.go        # 配置加载
│   ├── llm/
│   │   ├── client.go        # LLM客户端
│   │   └── embedding.go     # 向量嵌入
│   ├── memory/
│   │   └── manager.go       # 记忆系统管理
│   ├── models/
│   │   └── memory.go        # 数据模型
│   ├── onebot/
│   │   └── client.go        # OneBot WebSocket客户端
│   ├── persona/
│   │   └── persona.go       # 人格定义
│   ├── server/
│   │   └── server.go        # HTTP服务
│   └── tools/
│       └── registry.go      # MCP工具注册
├── data/                    # 数据目录（自动创建）
├── go.mod
└── README.md
```

## 快速开始

### 1. 环境准备

- Go 1.21+
- NapCat（QQ机器人框架）
- OpenAI API 或兼容API

### 2. 配置

复制并编辑配置文件：

```bash
cp config/config.yaml config/config.local.yaml
```

主要配置项：

```yaml
# OneBot配置
onebot:
  ws_url: "ws://127.0.0.1:3001"  # NapCat WebSocket地址

# 监听的群
groups:
  - group_id: 123456789
    enabled: true
    speak_probability: 0.3

# LLM配置
llm:
  api_key: "your-api-key"
  base_url: "https://api.openai.com/v1"
  model: "gpt-4o"
```

也可以通过环境变量配置敏感信息：

```bash
export AMU_LLM_API_KEY="your-api-key"
export AMU_ONEBOT_TOKEN="your-token"
```

### 3. 运行

```bash
# 安装依赖
go mod tidy

# 运行
go run cmd/amu/main.go

# 或者编译后运行
go build -o amu cmd/amu/main.go
./amu
```

## 架构设计

### Agent循环：Observer + Think + Act

```
┌─────────────────────────────────────────────────────────┐
│                    Agent Loop                            │
│                                                          │
│  ┌──────────┐    ┌──────────┐    ┌──────────┐          │
│  │ Observer │───▶│  Think   │───▶│   Act    │          │
│  │ 观察群聊  │    │ 思考决策  │    │ 执行动作  │          │
│  └──────────┘    └──────────┘    └──────────┘          │
│       │                │                │                │
│       ▼                ▼                ▼                │
│  消息缓冲区       LLM推理          发言/沉默             │
│  记忆检索        工具调用          保存记忆              │
│                                                          │
└─────────────────────────────────────────────────────────┘
```

### 记忆系统

| 记忆类型 | 说明 | 存储策略 |
|---------|------|---------|
| group_fact | 群长期事实 | 谁是管理员、群主题、常见话题 |
| member_profile | 成员画像 | 说话风格、爱好、常用词 |
| self_experience | 阿沐自身经历 | 之前参与的对话 |
| conversation | 对话记忆 | 重要的群聊片段 |

### MCP工具

| 工具名 | 说明 |
|-------|------|
| saveMemory | 保存重要信息到长期记忆 |
| queryMemory | 搜索相关记忆 |
| updateMemberProfile | 更新对群友的了解 |
| getMemberInfo | 查看群友信息 |
| speak | 在群里发言 |
| stayQuiet | 保持沉默 |
| getCurrentTime | 获取当前时间 |

## 人格设定

**名字**：阿沐（amu）

**性格特征**：
- 安静
- 偶尔吐槽
- 不抢话
- 不刷屏

**行为特征**：
- 经常"看着不说话"
- 只在自己熟悉或感兴趣的话题中插一句
- 不会次次都回人

**语言风格**：
- 偏口语化
- 不使用"作为一个AI"
- 不解释自己
- 不使用markdown

## 配置说明

### Agent决策参数

```yaml
agent:
  observe_window: 60        # 观察窗口（秒）
  think_interval: 30        # 思考间隔（秒）
  message_buffer_size: 50   # 消息缓冲区大小
  mention_response_prob: 0.85  # 被@时的响应概率
  topic_response_prob: 0.5     # 话题相关时的响应概率
  random_speak_prob: 0.1       # 随机插话概率
  speak_cooldown: 60           # 发言冷却（秒）
```

### 记忆系统参数

```yaml
memory:
  db_path: "./data/amu_memory.db"
  short_term:
    max_messages: 100
    expire_minutes: 30
  long_term:
    vector_dim: 1536
    top_k: 10
    similarity_threshold: 0.7
  importance_threshold: 0.5
```

## API接口

启动后可访问以下接口：

- `GET /health` - 健康检查
- `GET /api/status` - 运行状态
- `GET /api/memories` - 记忆列表
- `GET /api/members` - 成员画像列表

## License

MIT
