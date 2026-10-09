package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"sync"
	"time"
)

const SessionCookieName = "mumu_admin_session"

type Manager struct {
	adminKey string
	ttl      time.Duration

	mu       sync.RWMutex
	sessions map[string]time.Time
}

func NewManager(adminKey string, ttl time.Duration) *Manager {
	if ttl <= 0 {
		ttl = 24 * time.Hour
	}
	return &Manager{
		adminKey: adminKey,
		ttl:      ttl,
		sessions: make(map[string]time.Time),
	}
}

func (m *Manager) Enabled() bool {
	return m != nil && m.adminKey != ""
}

func (m *Manager) CheckKey(input string) bool {
	if !m.Enabled() {
		return false
	}
	if len(input) != len(m.adminKey) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(input), []byte(m.adminKey)) == 1
}

func (m *Manager) CreateSession() (string, time.Time, error) {
	if !m.Enabled() {
		return "", time.Time{}, errors.New("admin auth disabled")
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", time.Time{}, err
	}

	token := base64.RawURLEncoding.EncodeToString(raw)
	expiresAt := time.Now().Add(m.ttl)

	m.mu.Lock()
	// 顺手清理其他已过期的会话，避免长期运行时无访问的过期令牌堆积
	now := time.Now()
	for token, expires := range m.sessions {
		if now.After(expires) {
			delete(m.sessions, token)
		}
	}
	m.sessions[token] = expiresAt
	m.mu.Unlock()

	return token, expiresAt, nil
}

func (m *Manager) IsAuthenticated(token string) bool {
	if token == "" {
		return false
	}

	m.mu.RLock()
	expiresAt, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return false
	}

	if time.Now().After(expiresAt) {
		m.DeleteSession(token)
		return false
	}

	return true
}

func (m *Manager) DeleteSession(token string) {
	if token == "" {
		return
	}

	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}
