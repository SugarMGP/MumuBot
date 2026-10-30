package app

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"mumu-bot/internal/config"
	"mumu-bot/internal/secrets"
	"mumu-bot/internal/web/views"
)

func (a *App) settingsPageData(r *http.Request, errorText string) views.SettingsPageData {
	return views.SettingsPageData{
		Current:          *config.Snapshot(),
		Versions:         config.SectionVersions(),
		AgentReady:       a.mumuAgent != nil && a.mumuAgent.Ready(),
		SecretConfigured: secrets.Configured(),
		Error:            errorText,
		Flash:            a.flashFromRequest(r),
	}
}

func (a *App) handleSettings(w http.ResponseWriter, r *http.Request) {
	a.render(w, views.SettingsPage(a.settingsPageData(r, ""), r.URL.Path))
}

func (a *App) handleSettingsSave(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		a.renderSettingsError(w, r, "请求格式不正确。")
		return
	}
	section := strings.TrimSpace(r.FormValue("section"))
	version := parseInt64Query(r.FormValue("version"))
	if r.FormValue("action") == "reset" {
		if err := config.ResetSection(r.Context(), section, version); err != nil {
			a.renderSettingsError(w, r, err.Error())
			return
		}
		if a.mumuAgent != nil {
			next := config.Snapshot()
			a.mumuAgent.SetConcurrencyLimits(next.Agent.MaxGroupConcurrency, next.Agent.MaxPrivateConcurrency)
			if a.mumuAgent.Ready() {
				if err := a.mumuAgent.Reload(); err != nil {
					a.renderSettingsError(w, r, "已恢复默认值，但运行态应用失败："+err.Error())
					return
				}
			}
		}
		a.redirectFlash(w, r, "/admin/settings", "success", "已恢复默认值", "")
		return
	}
	old := *config.Snapshot()
	var value any
	var err error
	switch section {
	case config.SectionPersona:
		value = config.PersonaConfig{
			Name:                  strings.TrimSpace(r.FormValue("name")),
			AliasNames:            splitLines(r.FormValue("aliases")),
			Interests:             splitLines(r.FormValue("interests")),
			GroupPromptTemplate:   strings.TrimSpace(r.FormValue("group_prompt")),
			PrivatePromptTemplate: strings.TrimSpace(r.FormValue("private_prompt")),
		}
	case config.SectionAgent:
		value = config.AgentConfig{
			ThinkInterval:         parseIntValue(r.FormValue("think_interval")),
			ThinkDebounceMS:       parseIntValue(r.FormValue("think_debounce_ms")),
			MaxStep:               parseIntValue(r.FormValue("max_step")),
			MaxGroupConcurrency:   parseIntValue(r.FormValue("max_group_concurrency")),
			MaxPrivateConcurrency: parseIntValue(r.FormValue("max_private_concurrency")),
		}
	case config.SectionChat:
		rules, parseErr := parseTimeRules(r.FormValue("time_rules"))
		if parseErr != nil {
			a.renderSettingsError(w, r, parseErr.Error())
			return
		}
		value = config.ChatConfig{
			TalkFrequency:    parseFloatValue(r.FormValue("talk_frequency")),
			TypingSimulation: r.FormValue("typing_simulation") != "",
			TypingSpeed:      parseIntValue(r.FormValue("typing_speed")),
			EnableTimeRules:  r.FormValue("enable_time_rules") != "",
			TimeRules:        rules,
			RateLimit: config.RateLimitConfig{
				Enabled:     r.FormValue("rate_enabled") != "",
				PeriodSec:   parseIntValue(r.FormValue("period_sec")),
				MaxMessages: parseIntValue(r.FormValue("max_messages")),
				MinProb:     parseFloatValue(r.FormValue("min_prob")),
			},
		}
	case config.SectionCompactor:
		value = config.CompactorConfig{
			TriggerRatio:    parseFloatValue(r.FormValue("trigger_ratio")) / 100,
			TargetRatio:     parseFloatValue(r.FormValue("target_ratio")) / 100,
			RecentTailRatio: parseFloatValue(r.FormValue("recent_tail_ratio")) / 100,
			MaxSteps:        parseIntValue(r.FormValue("max_steps")),
		}
	case config.SectionMedia:
		value = config.MediaConfig{
			MaxBytesMB:   parseIntValue(r.FormValue("max_bytes_mb")),
			ImageQuality: parseIntValue(r.FormValue("image_quality")),
			NarrowEdge:   parseIntValue(r.FormValue("narrow_edge")),
		}
	case config.SectionDebug:
		value = config.DebugConfig{ShowToolCalls: r.FormValue("show_tool_calls") != ""}
	default:
		a.renderSettingsError(w, r, "未识别的设置分类。")
		return
	}
	if err == nil {
		err = config.SaveSection(r.Context(), section, value, version)
	}
	if err != nil {
		a.renderSettingsError(w, r, err.Error())
		return
	}
	if a.mumuAgent != nil {
		next := config.Snapshot()
		a.mumuAgent.SetConcurrencyLimits(next.Agent.MaxGroupConcurrency, next.Agent.MaxPrivateConcurrency)
		needGeneration := section == config.SectionMedia ||
			(section == config.SectionAgent && old.Agent.MaxStep != next.Agent.MaxStep)
		if needGeneration && a.mumuAgent.Ready() {
			if err := a.mumuAgent.Reload(); err != nil {
				a.renderSettingsError(w, r, "设置已保存，但运行态应用失败："+err.Error())
				return
			}
		}
	}
	a.redirectFlash(w, r, "/admin/settings", "success", "设置已保存", "")
}

func (a *App) renderSettingsError(w http.ResponseWriter, r *http.Request, message string) {
	a.renderStatus(w, http.StatusBadRequest, views.SettingsPage(a.settingsPageData(r, message), r.URL.Path))
}

func (a *App) redirectFlash(w http.ResponseWriter, r *http.Request, target, kind, title, body string) {
	values := url.Values{}
	if kind != "" {
		values.Set("flash_kind", kind)
	}
	if title != "" {
		values.Set("flash_title", title)
	}
	if body != "" {
		values.Set("flash_body", body)
	}
	if encoded := values.Encode(); encoded != "" {
		target += "?" + encoded
	}
	http.Redirect(w, r, target, http.StatusSeeOther)
}

func splitLines(raw string) []string {
	var result []string
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			result = append(result, line)
		}
	}
	return result
}

func parseIntValue(raw string) int {
	value, _ := strconv.Atoi(strings.TrimSpace(raw))
	return value
}

func parseFloatValue(raw string) float64 {
	value, _ := strconv.ParseFloat(strings.TrimSpace(raw), 64)
	return value
}

func parseTimeRules(raw string) ([]config.TimeRuleConfig, error) {
	result := make([]config.TimeRuleConfig, 0)
	for index, line := range splitLines(raw) {
		parts := strings.Split(line, ",")
		if len(parts) != 3 {
			return nil, fmt.Errorf("时段规则第 %d 行格式应为 开始-结束,群号,频率", index+1)
		}
		groupID, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("时段规则第 %d 行群号无效", index+1)
		}
		talk, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil {
			return nil, fmt.Errorf("时段规则第 %d 行频率无效", index+1)
		}
		result = append(result, config.TimeRuleConfig{
			TimeRange: strings.TrimSpace(parts[0]),
			GroupID:   groupID,
			TalkValue: talk,
		})
	}
	return result, nil
}
