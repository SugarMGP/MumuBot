# AGENTS

本目录负责管理后台的 HTTP 层：JSON 接口、登录鉴权、后台业务查询和嵌入式前端产物。界面代码在仓库根目录的 `web/`。

## 入口与职责

- `app/app.go`：依赖、chi 路由、SPA 外壳与静态资源挂载。
- `app/api.go`、`app/api_errors.go`：统一响应信封、错误码映射、分页参数。
- `app/auth.go`：管理员密钥登录、会话 Cookie、同源写保护。
- `app/*.go`：按资源拆分的接口实现；`dto.go` 是响应结构，字段与 `web/src/api/types.ts` 一一对应。
- `app/topic_view.go`：话题摘要与差异的只读模型。
- `app/logs_stream.go`：日志 SSE。
- `auth/`：后台访问鉴权。
- `services/`：给接口使用的后台业务查询。
- `assets/embed.go`：内嵌 `assets/dist` 并设置缓存头。

## 接口约定

- 成功返回 `{"data": ...}`，列表为 `{"items","total","page","page_size"}`；已落库但运行态未应用时追加 `{"warning":{"code":"runtime_reload_failed"}}`。
- 失败返回 `{"error":{"code","message","details"}}`；`code` 是稳定机器码，前端按码本地化，`message` 只是兜底。
- 已保存但运行态未应用与保存失败必须使用不同状态；前端要同时展示“配置已保存”和“当前运行态仍是旧值”，不能把 warning 吞成成功 toast。
- 写接口校验同源 `Origin`；修改类写请求体使用 JSON。
- 时间返回 RFC3339；枚举返回原始值（如 `active`、`group`、`profile`），中文展示由前端负责。
- 不在 Go 里拼用户可见的中文句子；服务层已有的错误文本只作为 `message` 兜底。

## 静态资源

- `/assets/*` 从内嵌产物读取，文件名带内容哈希，设置一年 immutable 缓存。
- `/admin`、`/admin/*`、`/login` 返回 `index.html`，并设置 `no-store`。
- `/health` 保持原有 JSON 结构，容器健康检查依赖它。

## 修改流程

- 改接口时同步修改 `web/src/api/types.ts` 与 `web/src/api/client-endpoints.ts`，两边的字段名必须完全一致。
- 改前端后运行：

```bash
cd web && bun run typecheck && bun run lint && bun run lint:i18n && bun run build
```

- 改 Go 后运行 `go build ./...` 与 `go vet ./...`。
- 涉及 UI 的改动按根目录 `AGENTS.md` 的验收要求，用真实浏览器截图检查浅色/深色与中文/英文。

## 注意

- 不要在 `app/` 里重新引入模板渲染、HTMX 片段或 HTML 页面。
- 不要手工修改 `assets/dist/`。
- 后台错误提示保留真实失败含义，但由前端错误码目录表达成使用者能看懂的话。
- 编辑表单的乐观并发版本必须与当前草稿基线绑定：刷新列表不能只替换版本号而保留旧正文，也不能用服务端新版本静默覆盖未保存草稿。保存冲突、重新拉取和放弃草稿都要有明确反馈。
- 有未保存草稿的模型、MCP、设置分区和人格编辑必须覆盖侧栏导航、浏览器后退与页面离开；不能只在关闭右侧编辑面板时确认。共享行为写在一处，不为每个页面复制一套表单框架。
