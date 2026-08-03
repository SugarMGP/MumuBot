package memory

import (
	"context"
	"strings"

	pgvector "github.com/pgvector/pgvector-go"
)

const (
	activeContextVectorThreshold = 0.7
	contextTextThreshold         = 0.1
)

// HybridQuery is one fixed-snapshot semantic query shared by topic and memory retrieval.
type HybridQuery struct {
	fragments []string
	embedding pgvector.Vector
}

func (q HybridQuery) Empty() bool {
	return len(q.fragments) == 0
}

func (q HybridQuery) Fragments() []string {
	return q.fragments
}

func (q HybridQuery) Vector() pgvector.Vector {
	return q.embedding
}

func (m *Manager) PrepareHybridQuery(ctx context.Context, fragments []string) (HybridQuery, error) {
	cleaned := make([]string, 0, len(fragments))
	for _, fragment := range fragments {
		if text := strings.TrimSpace(fragment); text != "" {
			cleaned = append(cleaned, text)
		}
	}
	if len(cleaned) == 0 {
		return HybridQuery{}, nil
	}

	text := strings.Join(cleaned, "\n")
	embedding, err := m.embedding.Embed(ctx, text)
	if err != nil {
		return HybridQuery{}, err
	}
	vector, err := EmbeddingVector(embedding)
	if err != nil {
		return HybridQuery{}, err
	}
	return HybridQuery{fragments: cleaned, embedding: vector}, nil
}

func (m *Manager) RecallContext(ctx context.Context, groupID int64, query HybridQuery) ([]Memory, []Memory, error) {
	if groupID == 0 || query.Empty() {
		return nil, nil, nil
	}

	local, err := m.searchPreparedMemories(ctx, query, groupID, 0, "", 4, activeContextVectorThreshold)
	if err != nil {
		return nil, nil, err
	}

	crossLimit := 0
	switch len(local) {
	case 0:
		crossLimit = 2
	case 1:
		crossLimit = 1
	}
	if crossLimit == 0 {
		return local, nil, nil
	}

	candidates, err := m.searchPreparedMemories(ctx, query, 0, groupID, MemoryScopeSelf, 4, activeContextVectorThreshold)
	if err != nil {
		return local, nil, err
	}
	seen := make(map[uint]struct{}, len(local))
	for _, item := range local {
		seen[item.ID] = struct{}{}
	}
	cross := make([]Memory, 0, crossLimit)
	for _, item := range candidates {
		if _, exists := seen[item.ID]; exists {
			continue
		}
		seen[item.ID] = struct{}{}
		cross = append(cross, item)
		if len(cross) == crossLimit {
			break
		}
	}
	return local, cross, nil
}

func (m *Manager) searchPreparedMemories(ctx context.Context, query HybridQuery, groupID, excludedGroupID int64, scope MemoryScope, limit int, vectorThreshold float64) ([]Memory, error) {
	if query.Empty() || limit <= 0 {
		return nil, nil
	}

	base := "status = 'active'"
	baseArgs := make([]any, 0, 2)
	if groupID != 0 {
		base += " AND group_id = ?"
		baseArgs = append(baseArgs, groupID)
	}
	if excludedGroupID != 0 {
		base += " AND group_id <> ?"
		baseArgs = append(baseArgs, excludedGroupID)
	}
	if scope != "" {
		base += " AND scope = ?"
		baseArgs = append(baseArgs, scope)
	}

	var vectorRows []rankedIDRow
	vectorSQL := "SELECT id FROM memories WHERE " + base + " AND 1 - (embedding <=> ?) >= ? ORDER BY embedding <=> ? LIMIT 20"
	vectorArgs := append(append([]any{}, baseArgs...), query.embedding, vectorThreshold, query.embedding)
	if err := m.db.WithContext(ctx).Raw(vectorSQL, vectorArgs...).Scan(&vectorRows).Error; err != nil {
		return nil, err
	}

	var textRows []rankedIDRow
	textSQL := `SELECT id FROM (
		SELECT id, (SELECT max(greatest(
			word_similarity(memories.content, fragment),
			word_similarity(fragment, memories.content)
		)) FROM unnest(?::text[]) AS fragments(fragment)) score
		FROM memories WHERE ` + base + `
	) ranked WHERE score >= ? ORDER BY score DESC LIMIT 20`
	textArgs := append([]any{query.fragments}, baseArgs...)
	textArgs = append(textArgs, contextTextThreshold)
	if err := m.db.WithContext(ctx).Raw(textSQL, textArgs...).Scan(&textRows).Error; err != nil {
		return nil, err
	}

	ids := fuseRRF(rankRows(vectorRows), rankRows(textRows))
	if len(ids) > limit {
		ids = ids[:limit]
	}
	return m.loadMemoriesInOrder(ctx, ids)
}
