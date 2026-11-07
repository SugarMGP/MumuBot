package app

import (
	"errors"
	"net/http"

	"mumu-bot/internal/config"
	"mumu-bot/internal/mcp"
	"mumu-bot/internal/memory"
	"mumu-bot/internal/modelprofiles"

	"go.uber.org/zap"
	"gorm.io/gorm"
)

const (
	messageVersionConflict = "内容已被其他页面修改，请刷新后重试。"
	messageNotFound        = "记录不存在或已被删除。"
	messageValidation      = "提交的内容不合法，请检查后重试。"
	messageInternal        = "操作未完成，请稍后再试。"
)

// failFromError 把服务层错误映射为稳定的错误码；前端按 code 做本地化
func (a *App) failFromError(w http.ResponseWriter, r *http.Request, err error) {
	var validation *config.ValidationError
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound),
		errors.Is(err, modelprofiles.ErrNotFound),
		errors.Is(err, mcp.ErrNotFound),
		errors.Is(err, memory.ErrPersonaPresetNotFound):
		respondError(w, http.StatusNotFound, "not_found", messageNotFound, nil)
	case errors.Is(err, config.ErrVersionConflict),
		errors.Is(err, modelprofiles.ErrVersionConflict),
		errors.Is(err, mcp.ErrVersionConflict),
		errors.Is(err, memory.ErrPersonaPresetVersion):
		respondError(w, http.StatusConflict, "version_conflict", messageVersionConflict, nil)
	case errors.Is(err, memory.ErrPersonaPresetDuplicateName):
		respondError(w, http.StatusBadRequest, "preset_duplicate", "已经有同名预设了。", nil)
	case errors.Is(err, memory.ErrPersonaPresetInvalid), errors.Is(err, mcp.ErrInvalidConfig):
		respondError(w, http.StatusBadRequest, "validation_failed", messageValidation, nil)
	case errors.Is(err, memory.ErrScheduleTextInvalid):
		respondError(w, http.StatusBadRequest, lifeScheduleTextInvalid, "", nil)
	case errors.Is(err, memory.ErrPersonaPresetBuiltin):
		respondError(w, http.StatusBadRequest, "preset_builtin", "内置预设不能删除。", nil)
	case errors.Is(err, memory.ErrPersonaPresetActive):
		respondError(w, http.StatusBadRequest, "preset_active", "正在使用的预设不能删除。", nil)
	case errors.Is(err, modelprofiles.ErrActiveModel):
		respondError(w, http.StatusBadRequest, "model_active", "当前正在使用的模型不能删除。", nil)
	case errors.Is(err, modelprofiles.ErrInvalidModel):
		respondError(w, http.StatusBadRequest, "validation_failed", messageValidation, nil)
	case errors.As(err, &validation):
		respondError(w, http.StatusBadRequest, "validation_failed", validation.Message, nil)
	default:
		zap.L().Error("后台操作失败", zap.String("path", r.URL.Path), zap.Error(err))
		respondError(w, http.StatusInternalServerError, "internal", messageInternal, nil)
	}
}

func (a *App) failBadRequest(w http.ResponseWriter, message string) {
	if message == "" {
		message = messageValidation
	}
	respondError(w, http.StatusBadRequest, "validation_failed", message, nil)
}

func (a *App) failSecretUnavailable(w http.ResponseWriter) {
	respondError(w, http.StatusBadRequest, "secret_unavailable", "加密密钥尚未配置，暂时不能保存。", nil)
}
