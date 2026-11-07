package persona

import (
	"regexp"
	"strconv"
	"strings"

	"mumu-bot/internal/config"
)

// Identity 是提示词模板可用的全局身份值
type Identity struct {
	Name         string
	MasterName   string
	MasterUserID int64
}

// IdentityFromConfig 把持久化的身份配置转成模板值
func IdentityFromConfig(cfg config.IdentityConfig) Identity {
	return Identity{
		Name:         strings.TrimSpace(cfg.Name),
		MasterName:   strings.TrimSpace(cfg.MasterName),
		MasterUserID: cfg.MasterUserID,
	}
}

var templatePattern = regexp.MustCompile(`<(name|master_name|master_qq|master)>`)

// Render 替换提示词里的身份占位符，未知占位符保持原样
func Render(text string, identity Identity) string {
	if text == "" {
		return text
	}
	name := identity.Name
	if name == "" {
		name = "沐沐"
	}
	masterName := identity.MasterName
	masterQQ := "未设置"
	if identity.MasterUserID > 0 {
		masterQQ = strconv.FormatInt(identity.MasterUserID, 10)
	}
	if masterName == "" && identity.MasterUserID > 0 {
		masterName = masterQQ
	}
	if masterName == "" {
		masterName = "未设置"
	}
	master := "你还没有指定主人。"
	if identity.MasterUserID > 0 {
		master = "你的主人是 " + masterName + "（QQ " + masterQQ + "）。"
	}
	return templatePattern.ReplaceAllStringFunc(text, func(match string) string {
		switch match {
		case "<name>":
			return name
		case "<master_name>":
			return masterName
		case "<master_qq>":
			return masterQQ
		case "<master>":
			return master
		default:
			return match
		}
	})
}

// RenderFor 用配置快照里的身份渲染文本
func RenderFor(text string, cfg *config.RuntimeConfig) string {
	if cfg == nil {
		return text
	}
	return Render(text, IdentityFromConfig(cfg.Identity))
}
