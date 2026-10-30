package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Bootstrap 是进程启动期不可变的连接与密钥配置
type Bootstrap struct {
	DatabaseDSN     string
	WebAdminKey     string
	ServerPort      int
	OneBotWSURL     string
	OneBotToken     string
	OneBotReconnect int
	MediaDir        string
	StickerDir      string
	LogLevel        string
	Debug           bool
	SecretKey       string
}

var boot Bootstrap

// LoadBootstrap 从环境变量读取启动配置
func LoadBootstrap() (Bootstrap, error) {
	b := Bootstrap{
		DatabaseDSN:     strings.TrimSpace(os.Getenv("MUMU_DATABASE_DSN")),
		WebAdminKey:     os.Getenv("MUMU_WEB_ADMIN_KEY"),
		ServerPort:      intEnv("MUMU_SERVER_PORT", 7468),
		OneBotWSURL:     strings.TrimSpace(os.Getenv("MUMU_ONEBOT_WS_URL")),
		OneBotToken:     os.Getenv("MUMU_ONEBOT_TOKEN"),
		OneBotReconnect: intEnv("MUMU_ONEBOT_RECONNECT_SECONDS", 5),
		MediaDir:        strings.TrimSpace(os.Getenv("MUMU_MEDIA_DIR")),
		StickerDir:      strings.TrimSpace(os.Getenv("MUMU_STICKER_DIR")),
		LogLevel:        strings.TrimSpace(os.Getenv("MUMU_LOG_LEVEL")),
		Debug:           boolEnv("MUMU_DEBUG"),
		SecretKey:       strings.TrimSpace(os.Getenv("MUMU_SECRET_KEY")),
	}
	if b.MediaDir == "" {
		b.MediaDir = "./media"
	}
	if b.StickerDir == "" {
		b.StickerDir = "./stickers"
	}
	if b.LogLevel == "" {
		b.LogLevel = "info"
	}
	if b.DatabaseDSN == "" {
		return b, fmt.Errorf("MUMU_DATABASE_DSN 不能为空")
	}
	if b.OneBotWSURL == "" {
		return b, fmt.Errorf("MUMU_ONEBOT_WS_URL 不能为空")
	}
	if b.ServerPort <= 0 || b.ServerPort > 65535 {
		return b, fmt.Errorf("MUMU_SERVER_PORT 必须在 1 到 65535 之间")
	}
	if b.OneBotReconnect <= 0 {
		return b, fmt.Errorf("MUMU_ONEBOT_RECONNECT_SECONDS 必须大于 0")
	}
	boot = b
	return b, nil
}

// Boot 返回启动期配置
func Boot() Bootstrap {
	return boot
}

func intEnv(name string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func boolEnv(name string) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return false
	}
	value, err := strconv.ParseBool(raw)
	return err == nil && value
}
