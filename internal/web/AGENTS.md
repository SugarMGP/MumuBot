# AGENTS

本目录负责管理后台：HTTP 路由、登录鉴权、页面模板、前端资源、后台服务和嵌入式静态资源。

## 入口与职责

- `app/app.go`：应用依赖和 chi 路由；`pages.go`、`actions.go`、`system.go` 分别承载页面、管理动作和系统信息处理器。
- `auth/`：后台访问鉴权。
- `services/`：给页面和接口使用的后台业务查询。
- `views/*.templ`：templ 页面模板源码。
- `views/*_templ.go`：templ 生成产物，禁止手动修改。
- `assets/src/`：前端资源源码。
- `assets/dist/`：前端构建产物，禁止手动修改。

## 页面与文案

- 后台文案面向使用者，不暴露文件路径、配置键名、构建方式、源码术语或是否写库。
- 状态、类型、枚举值必须转成可读中文。
- 文案描述“当前能看到什么、能做什么、状态如何”，不要描述实现细节。
- 页面交互优先使用 Preline（界面组件库）和 Alpine（轻量前端交互框架）的推荐写法。

## 修改流程

- 修改 `.templ` 后运行：

```bash
templ generate ./internal/web/views
```

- 修改 `assets/src/` 后运行：

```bash
npm run build
```

- 涉及后台 UI 的改动，交付前使用 Codex 内置浏览器做真实页面验证，包含截图、交互、控制台和网络请求检查；不要使用 Chrome DevTools MCP。

## 修改注意

- 不手工编辑 `views/*_templ.go` 和 `assets/dist/`。
- 不要为了一个页面的临时样式新建全局模式；多个页面稳定复用后再提炼。
- 后台错误提示要保留真实失败含义，但表达成使用者能理解的中文。
