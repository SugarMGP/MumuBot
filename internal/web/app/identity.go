package app

import (
	"fmt"
	"net/http"
	"strings"
	"unicode/utf8"

	"mumu-bot/internal/config"
	"mumu-bot/internal/memory"
)

type identityDTO struct {
	Name            string `json:"name"`
	MasterUserID    int64  `json:"master_user_id"`
	MasterName      string `json:"master_name"`
	MasterAvailable bool   `json:"master_available"`
	Version         int64  `json:"version"`
}

type identityFriendDTO struct {
	UserID  int64  `json:"user_id"`
	Name    string `json:"name"`
	Active  bool   `json:"active"`
	Blocked bool   `json:"blocked"`
}

type identityFriendsResponse struct {
	Items []identityFriendDTO `json:"items"`
}

type identityInput struct {
	Name         string `json:"name"`
	MasterUserID int64  `json:"master_user_id"`
	Version      int64  `json:"version"`
}

func (a *App) identityData(r *http.Request) identityDTO {
	cfg := config.Snapshot()
	out := identityDTO{
		Name: cfg.Identity.Name, MasterUserID: cfg.Identity.MasterUserID,
		MasterName: cfg.Identity.MasterName, Version: config.SectionVersions()[config.SectionIdentity],
	}
	if a.memMgr != nil && out.MasterUserID > 0 {
		if target, err := a.memMgr.GetConversationTarget(r.Context(), memory.ConversationKindPrivate, out.MasterUserID); err == nil && target != nil && target.Active && !target.Blocked {
			out.MasterAvailable = true
		}
	}
	return out
}

func (a *App) handleIdentity(w http.ResponseWriter, r *http.Request) {
	respondData(w, http.StatusOK, a.identityData(r), nil)
}

func (a *App) handleIdentityFriends(w http.ResponseWriter, r *http.Request) {
	if a.memMgr == nil {
		respondError(w, http.StatusServiceUnavailable, "internal", "", nil)
		return
	}
	rows, err := a.memMgr.ListConversationTargets(r.Context(), memory.ConversationKindPrivate, false)
	if err != nil {
		a.failFromError(w, r, err)
		return
	}
	out := identityFriendsResponse{Items: make([]identityFriendDTO, 0, len(rows))}
	for _, row := range rows {
		out.Items = append(out.Items, identityFriendDTO{
			UserID: row.TargetID, Name: row.Name, Active: row.Active, Blocked: row.Blocked,
		})
	}
	respondData(w, http.StatusOK, out, nil)
}

func (a *App) handleIdentityUpdate(w http.ResponseWriter, r *http.Request) {
	var body identityInput
	if err := decodeJSON(r, &body); err != nil {
		a.failIdentity(w, "validation_failed")
		return
	}
	name := strings.TrimSpace(body.Name)
	if name == "" || utf8.RuneCountInString(name) > 32 {
		a.failIdentity(w, "identity_name_invalid")
		return
	}
	if body.MasterUserID < 0 {
		a.failIdentity(w, "identity_master_invalid")
		return
	}
	current := config.Snapshot().Identity
	masterName := ""
	if body.MasterUserID > 0 {
		if body.MasterUserID == current.MasterUserID {
			// 好友被删除后主人保持不变，改角色名或重新保存不能把它清掉
			masterName = current.MasterName
			if a.memMgr != nil {
				if target, err := a.memMgr.GetConversationTarget(r.Context(), memory.ConversationKindPrivate, body.MasterUserID); err == nil && target != nil && strings.TrimSpace(target.Name) != "" {
					masterName = strings.TrimSpace(target.Name)
				}
			}
			if masterName == "" {
				masterName = fmt.Sprintf("QQ %d", body.MasterUserID)
			}
		} else {
			if a.memMgr == nil {
				a.failIdentity(w, "identity_master_invalid")
				return
			}
			target, err := a.memMgr.GetConversationTarget(r.Context(), memory.ConversationKindPrivate, body.MasterUserID)
			if err != nil || target == nil || !target.Active || target.Blocked {
				a.failIdentity(w, "identity_master_invalid")
				return
			}
			masterName = strings.TrimSpace(target.Name)
			if masterName == "" {
				masterName = fmt.Sprintf("QQ %d", body.MasterUserID)
			}
		}
	}
	cfg := current
	cfg.Name = name
	cfg.MasterUserID = body.MasterUserID
	cfg.MasterName = masterName
	if err := config.SaveSection(r.Context(), config.SectionIdentity, cfg, body.Version); err != nil {
		a.failFromError(w, r, err)
		return
	}
	respondData(w, http.StatusOK, a.identityData(r), a.reloadPersonaWarning())
}

func (a *App) failIdentity(w http.ResponseWriter, code string) {
	respondError(w, http.StatusBadRequest, code, "", nil)
}
