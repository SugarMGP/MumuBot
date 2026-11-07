# AGENTS

本目录是 worker 容器的私网执行服务：现有 `/v1/exec` 运行命令，`/v1/files/*` 只在唯一 `/home/worker` 内读写文件。

- `server.go`：启动时确定唯一账号、home、shell 和 `os.Root`；worker-exec 以 worker 身份启动。
- `files.go`：单层列目录、UTF-8 文本窗口、create/append/replace、HTTP(S) 下载落盘，以及 `GET/PUT /v1/files/content` 的 32 MiB 原始字节流读取与导入。

## 边界

- 所有文件路径都相对 home；绝对路径、上跳、空路径和越界符号链接必须拒绝，不能靠字符串前缀判断。
- 文本读取最多 1 MiB/8000 字符，写入最多 1 MiB，单次字节读取/上传最多 32 MiB；超限返回真实原因，不静默截断成功。
- 文件操作和普通命令以 worker 身份运行；sudo 子命令的进程组取消失败时使用参数数组的 `sudo -n /bin/kill`，不能拼接 shell。
- 文件服务和执行服务共用现有私网端口，不新增公网端口、Mumu home 挂载或公开下载页。
- 所有 worker session 共用唯一账号和 home，这只是并行子 Agent 的共享执行环境，不是 worker 之间的权限隔离或互斥锁；提示词必须要求各自使用明确任务目录和 tmux 名，不得接管、修改或停止别人的进行中任务。
- 跨调用后台任务统一使用 `bash + tmux`：固定会话名、日志、退出码文件和显式检查/交接/停止方式。不新增 job registry、PTY、自动完成通知或进程恢复服务；取消模型回合不会自动终止已脱离的 tmux 任务。
- bash 已产生的 stdout/stderr 必须如实返回；单流超过 64 KiB 时采用有界 head+tail 捕获，说明实际字节数、保留范围和可续读的 home 日志路径，优先保留真实错误；不能只返回头部、截断标志或把保存日志失败说成命令未执行。

## 镜像基线

- worker 镜像固定 Node 24.22.2、Bun 1.4.4、uv 0.13.7 和 Go 版 yq 4.54.2；JavaScript/TypeScript 新任务优先 Bun，依赖 Node 原生兼容的现有工具继续用 Node，不自动改写项目既有锁文件和声明运行时。
- 常用 CLI 在现有 `rg/jq/git/curl/bash/tmux/python` 基础上补 `fd-find`（实际命令 `fdfind`）、`bat`（实际命令 `batcat`）、`sd`、`file`、`tree`、`7zip`、`poppler-utils`。保留基础命令兼容性，`gh`、媒体、Office、OCR 和浏览器工具按需安装。
- 镜像基线可执行文件放 `/usr/local/bin` 等镜像层；不要只放在会被持久卷覆盖的 home/profile。若在 Dockerfile 设置 `NO_COLOR`/`PAGER`，还必须在共享命令执行入口真正传入，否则模型命令不会继承。
