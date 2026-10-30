package app

import (
	"net/http"
	"strconv"
	"strings"

	"mumu-bot/internal/mcp"
	"mumu-bot/internal/secrets"
	"mumu-bot/internal/web/views"
)

func (a *App) mcpPageData(r *http.Request, errorText string) views.MCPPageData {
	servers, err := a.mcpStore.List(r.Context())
	if err != nil && errorText == "" {
		errorText = err.Error()
	}
	data := views.MCPPageData{
		Servers:          servers,
		SecretConfigured: secrets.Configured(),
		Error:            errorText,
		Flash:            a.flashFromRequest(r),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("edit")); raw != "" {
		id, parseErr := strconv.ParseUint(raw, 10, 64)
		if parseErr == nil {
			cfg, row, getErr := a.mcpStore.Get(r.Context(), uint(id))
			if getErr == nil {
				data.Edit = &cfg
				data.EditID = row.ID
				data.EditVersion = row.Version
				data.EditPosition = row.Position
			}
		}
	}
	return data
}

func (a *App) handleMCP(w http.ResponseWriter, r *http.Request) {
	a.render(w, views.MCPPage(a.mcpPageData(r, ""), r.URL.Path))
}

func (a *App) handleMCPAction(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderMCPError(w, r, "请求格式不正确。")
		return
	}
	switch strings.TrimSpace(r.FormValue("action")) {
	case "save":
		a.saveMCPServer(w, r)
	case "delete":
		a.deleteMCPServer(w, r)
	default:
		a.renderMCPError(w, r, "未识别的 MCP 操作。")
	}
}

func (a *App) saveMCPServer(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	version := parseInt64Query(r.FormValue("version"))
	position := parseIntValue(r.FormValue("position"))
	cfg := mcp.ServerConfig{
		Name:          strings.TrimSpace(r.FormValue("name")),
		Enabled:       r.FormValue("enabled") != "",
		Type:          strings.TrimSpace(r.FormValue("type")),
		URL:           strings.TrimSpace(r.FormValue("url")),
		Command:       strings.TrimSpace(r.FormValue("command")),
		Args:          splitLines(r.FormValue("args")),
		Env:           splitLines(r.FormValue("env")),
		ToolNameList:  splitComma(r.FormValue("tool_names")),
		CustomHeaders: parseKeyValues(r.FormValue("headers")),
	}
	if _, err := a.mcpStore.Save(r.Context(), id, cfg, version, position); err != nil {
		a.renderMCPError(w, r, err.Error())
		return
	}
	if a.mumuAgent != nil && a.mumuAgent.Ready() {
		if err := a.mumuAgent.Reload(); err != nil {
			a.renderMCPError(w, r, "MCP 配置已保存，但运行态应用失败，继续使用旧连接："+err.Error())
			return
		}
	}
	a.redirectFlash(w, r, "/admin/settings/mcp", "success", "MCP 配置已保存", "")
}

func (a *App) deleteMCPServer(w http.ResponseWriter, r *http.Request) {
	id := uint(parseInt64Query(r.FormValue("id")))
	version := parseInt64Query(r.FormValue("version"))
	if err := a.mcpStore.Delete(r.Context(), id, version); err != nil {
		a.renderMCPError(w, r, err.Error())
		return
	}
	if a.mumuAgent != nil && a.mumuAgent.Ready() {
		if err := a.mumuAgent.Reload(); err != nil {
			a.renderMCPError(w, r, "MCP 已删除，但运行态应用失败："+err.Error())
			return
		}
	}
	a.redirectFlash(w, r, "/admin/settings/mcp", "success", "MCP 服务器已删除", "")
}

func (a *App) renderMCPError(w http.ResponseWriter, r *http.Request, message string) {
	a.renderStatus(w, http.StatusBadRequest, views.MCPPage(a.mcpPageData(r, message), r.URL.Path))
}

func splitComma(raw string) []string {
	var result []string
	for _, part := range strings.Split(raw, ",") {
		if value := strings.TrimSpace(part); value != "" {
			result = append(result, value)
		}
	}
	return result
}

func parseKeyValues(raw string) map[string]string {
	result := make(map[string]string)
	for _, line := range splitLines(raw) {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			result[key] = strings.TrimSpace(value)
		}
	}
	return result
}
