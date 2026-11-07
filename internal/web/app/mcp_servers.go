package app

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"mumu-bot/internal/mcp"
	"mumu-bot/internal/modelprofiles"
	"mumu-bot/internal/secrets"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
)

type mcpInput struct {
	Name      string            `json:"name"`
	Enabled   bool              `json:"enabled"`
	Type      string            `json:"type"`
	URL       string            `json:"url"`
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       []string          `json:"env"`
	Headers   map[string]string `json:"headers"`
	ToolNames []string          `json:"tool_names"`
	Version   int64             `json:"version"`
}

func (a *App) handleMCPServers(w http.ResponseWriter, r *http.Request) {
	rows, err := a.mcpStore.List(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	items := make([]mcpServerSummaryDTO, 0, len(rows))
	for _, row := range rows {
		items = append(items, mcpServerSummary(row))
	}
	respondData(w, http.StatusOK, mcpServersResponse{
		Items:            items,
		SecretConfigured: secrets.Configured(),
	}, nil)
}

func (a *App) handleMCPServerDetail(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	cfg, row, err := a.mcpStore.Get(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, mcpServerDetail(cfg, row), nil)
}

func (a *App) handleMCPServerCreate(w http.ResponseWriter, r *http.Request) {
	if !secrets.Configured() {
		a.failSecretUnavailable(w)
		return
	}
	var body mcpInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	cfg := mcpConfigFromInput(body)
	rows, err := a.mcpStore.List(r.Context())
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	position := 0
	for _, row := range rows {
		if row.Position >= position {
			position = row.Position + 1
		}
	}
	saved, err := a.mcpStore.Save(r.Context(), 0, cfg, 0, position)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	storedCfg, storedRow, err := a.mcpStore.Get(r.Context(), saved.ID)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusCreated, mcpServerDetail(storedCfg, storedRow), a.reloadMCPWarning(r.Context()))
}

func (a *App) handleMCPServerUpdate(w http.ResponseWriter, r *http.Request) {
	if !secrets.Configured() {
		a.failSecretUnavailable(w)
		return
	}
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	var body mcpInput
	if err := decodeJSON(r, &body); err != nil {
		a.failBadRequest(w, "请求格式不正确。")
		return
	}
	_, existing, err := a.mcpStore.Get(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	cfg := mcpConfigFromInput(body)
	if _, err := a.mcpStore.Save(r.Context(), id, cfg, body.Version, existing.Position); err != nil {
		a.failFromError(w, r, err)
		return
	}
	storedCfg, storedRow, err := a.mcpStore.Get(r.Context(), id)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, mcpServerDetail(storedCfg, storedRow), a.reloadMCPWarning(r.Context()))
}

func (a *App) handleMCPServerDelete(w http.ResponseWriter, r *http.Request) {
	id, err := parseUintParam(chi.URLParam(r, "id"))
	if err != nil {
		respondError(w, http.StatusBadRequest, "validation_failed", "记录编号无效。", nil)
		return
	}
	version := queryInt64(r, "version")
	if err := a.mcpStore.Delete(r.Context(), id, version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, map[string]bool{"deleted": true}, a.reloadMCPWarning(r.Context()))
}

func mcpServerSummary(row mcp.StoredServer) mcpServerSummaryDTO {
	return mcpServerSummaryDTO{
		ID:        row.ID,
		Name:      row.Name,
		Enabled:   row.Enabled,
		Transport: row.Transport,
		Version:   row.Version,
		Position:  row.Position,
		UpdatedAt: row.UpdatedAt,
	}
}

func mcpServerDetail(cfg mcp.ServerConfig, row mcp.StoredServer) mcpServerDetailDTO {
	args := cfg.Args
	if args == nil {
		args = []string{}
	}
	env := cfg.Env
	if env == nil {
		env = []string{}
	}
	headers := cfg.CustomHeaders
	if headers == nil {
		headers = map[string]string{}
	}
	tools := cfg.ToolNameList
	if tools == nil {
		tools = []string{}
	}
	return mcpServerDetailDTO{
		mcpServerSummaryDTO: mcpServerSummary(row),
		Type:                cfg.Type,
		URL:                 cfg.URL,
		Command:             cfg.Command,
		Args:                args,
		Env:                 env,
		Headers:             headers,
		ToolNames:           tools,
	}
}

func mcpConfigFromInput(body mcpInput) mcp.ServerConfig {
	return mcp.ServerConfig{
		Name:          strings.TrimSpace(body.Name),
		Enabled:       body.Enabled,
		Type:          strings.TrimSpace(body.Type),
		URL:           strings.TrimSpace(body.URL),
		Command:       strings.TrimSpace(body.Command),
		Args:          body.Args,
		Env:           body.Env,
		ToolNameList:  body.ToolNames,
		CustomHeaders: body.Headers,
	}
}

func (a *App) reloadMCPWarning(ctx context.Context) *warningPayload {
	agent := a.currentAgent()
	if agent == nil {
		return nil
	}
	if _, err := a.modelStore.Active(ctx); err != nil {
		if errors.Is(err, modelprofiles.ErrNotFound) {
			return nil
		}
		zap.L().Warn("读取当前模型失败，暂不应用 MCP 配置", zap.Error(err))
		return &warningPayload{Code: "runtime_reload_failed"}
	}
	if err := agent.Reload(); err != nil {
		zap.L().Warn("MCP 配置已保存但运行态应用失败", zap.Error(err))
		return &warningPayload{Code: "runtime_reload_failed"}
	}
	for _, status := range agent.MCPStatuses() {
		if !status.Connected {
			return &warningPayload{Code: "mcp_partial_failure"}
		}
	}
	return nil
}
