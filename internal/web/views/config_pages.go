package views

import (
	"sort"
	"strconv"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/modelprofiles"
)

type SettingsPageData struct {
	Current          config.RuntimeConfig
	Versions         map[string]int64
	AgentReady       bool
	SecretConfigured bool
	Error            string
	Flash            *FlashMessage
}

type ModelPageData struct {
	Profiles         []modelprofiles.Profile
	Edit             *modelprofiles.Profile
	SecretConfigured bool
	Error            string
	Flash            *FlashMessage
}

type MCPPageData struct {
	Servers          []mcp.StoredServer
	Edit             *mcp.ServerConfig
	EditID           uint
	EditVersion      int64
	EditPosition     int
	SecretConfigured bool
	Error            string
	Flash            *FlashMessage
}

func TimeRulesText(rules []config.TimeRuleConfig) string {
	var b strings.Builder
	for _, rule := range rules {
		b.WriteString(rule.TimeRange)
		b.WriteString(",")
		b.WriteString(strconv.FormatInt(rule.GroupID, 10))
		b.WriteString(",")
		b.WriteString(strconv.FormatFloat(rule.TalkValue, 'f', 2, 64))
		b.WriteString("\n")
	}
	return b.String()
}

func ArgsText(items []string) string { return strings.Join(items, "\n") }

func EnvText(items []string) string { return strings.Join(items, "\n") }

func HeadersText(items map[string]string) string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteString("=")
		b.WriteString(items[key])
		b.WriteString("\n")
	}
	return b.String()
}

func ToolNamesText(items []string) string { return strings.Join(items, ", ") }

func reasoningSummary(thinking, effort string) string {
	parts := make([]string, 0, 2)
	if thinking != "" {
		parts = append(parts, "thinking="+thinking)
	}
	if effort != "" {
		parts = append(parts, "effort="+effort)
	}
	if len(parts) == 0 {
		return "默认"
	}
	return strings.Join(parts, " / ")
}

func modelName(profile *modelprofiles.Profile) string {
	if profile == nil {
		return ""
	}
	return profile.Name
}

func modelBaseURL(profile *modelprofiles.Profile) string {
	if profile == nil {
		return ""
	}
	return profile.BaseURL
}

func modelModelName(profile *modelprofiles.Profile) string {
	if profile == nil {
		return ""
	}
	return profile.ModelName
}

func modelContextWindow(profile *modelprofiles.Profile) string {
	if profile == nil {
		return "200000"
	}
	return strconv.Itoa(profile.ContextWindow)
}

func modelKeyPlaceholder(profile *modelprofiles.Profile) string {
	if profile == nil || profile.APIKeyHint == "" {
		return "sk-..."
	}
	return profile.APIKeyHint + "（留空保留）"
}

func modelThinking(profile *modelprofiles.Profile) string {
	if profile == nil {
		return ""
	}
	return profile.ThinkingType
}

func modelEffort(profile *modelprofiles.Profile) string {
	if profile == nil {
		return ""
	}
	return profile.ReasoningEffort
}

func modelSubmitLabel(profile *modelprofiles.Profile) string {
	if profile == nil {
		return "添加模型"
	}
	return "保存模型"
}

func mcpName(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Name
}

func mcpType(cfg *mcp.ServerConfig) string {
	if cfg == nil || cfg.Type == "" {
		return "sse"
	}
	return cfg.Type
}

func mcpURL(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.URL
}

func mcpCommand(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return cfg.Command
}

func mcpArgs(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return ArgsText(cfg.Args)
}

func mcpEnv(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return EnvText(cfg.Env)
}

func mcpHeaders(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return HeadersText(cfg.CustomHeaders)
}

func mcpToolNames(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return ""
	}
	return ToolNamesText(cfg.ToolNameList)
}

func mcpSubmitLabel(cfg *mcp.ServerConfig) string {
	if cfg == nil {
		return "添加服务器"
	}
	return "保存服务器"
}
