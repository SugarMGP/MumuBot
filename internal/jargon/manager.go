package jargon

import (
	"sync"

	"mumu-bot/internal/memory"

	ahocorasick "github.com/petar-dambovaliev/aho-corasick"
	"go.uber.org/zap"
)

type groupIndex struct {
	machine  ahocorasick.AhoCorasick
	patterns []string
	meanings []string
}

type Manager struct {
	memMgr *memory.Manager
	groups map[int64]groupIndex
	mu     sync.RWMutex
}

func New(memMgr *memory.Manager) *Manager {
	m := &Manager{memMgr: memMgr, groups: make(map[int64]groupIndex)}
	m.Reload()
	return m
}

func (m *Manager) Reload() {
	rows, err := m.memMgr.GetAllApprovedJargons()
	if err != nil {
		zap.L().Error("加载黑话失败", zap.Error(err))
		return
	}
	patterns := make(map[int64][]string)
	meanings := make(map[int64][]string)
	for _, row := range rows {
		patterns[row.GroupID] = append(patterns[row.GroupID], row.Term)
		meanings[row.GroupID] = append(meanings[row.GroupID], row.Meaning)
	}
	groups := make(map[int64]groupIndex, len(patterns))
	for groupID, groupPatterns := range patterns {
		groups[groupID] = groupIndex{machine: buildMachine(groupPatterns), patterns: groupPatterns, meanings: meanings[groupID]}
	}
	m.mu.Lock()
	m.groups = groups
	m.mu.Unlock()
}

func buildMachine(patterns []string) ahocorasick.AhoCorasick {
	builder := ahocorasick.NewAhoCorasickBuilder(ahocorasick.Opts{AsciiCaseInsensitive: true, MatchOnlyWholeWords: false, MatchKind: ahocorasick.LeftMostLongestMatch, DFA: true})
	return builder.Build(patterns)
}

func (m *Manager) Match(groupID int64, text string) map[string]string {
	m.mu.RLock()
	index, ok := m.groups[groupID]
	m.mu.RUnlock()
	if !ok || len(index.patterns) == 0 {
		return nil
	}
	result := make(map[string]string)
	for _, match := range index.machine.FindAll(text) {
		idx := match.Pattern()
		if idx >= 0 && idx < len(index.patterns) {
			result[index.patterns[idx]] = index.meanings[idx]
		}
	}
	return result
}
