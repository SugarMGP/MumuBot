package memory

import (
	"context"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
	pgvector "github.com/pgvector/pgvector-go"
)

const (
	contextVectorThreshold = 0.3
	contextTextThreshold   = 0.1
)

// HybridQuery 是基于固定消息快照构造的语义查询，由话题和记忆检索共用
type HybridQuery struct {
	fragments []string
	embedding pgvector.Vector
}

func (q HybridQuery) Empty() bool {
	return len(q.fragments) == 0
}

func (q HybridQuery) FragmentArray() pgtype.Array[string] {
	return pgtype.Array[string]{
		Elements: q.fragments,
		Dims:     []pgtype.ArrayDimension{{Length: int32(len(q.fragments)), LowerBound: 1}},
		Valid:    true,
	}
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

func int64Array(values []int64) pgtype.Array[int64] {
	return pgtype.Array[int64]{
		Elements: values,
		Dims:     []pgtype.ArrayDimension{{Length: int32(len(values)), LowerBound: 1}},
		Valid:    true,
	}
}

// FuseRanks 融合按相关性排列的召回列表，同分时按内部 ID 保持稳定顺序
func FuseRanks(lists ...[]uint) []uint {
	scores := make(map[uint]float64)
	for _, list := range lists {
		for i, id := range list {
			scores[id] += 1 / float64(61+i)
		}
	}
	ids := make([]uint, 0, len(scores))
	for id := range scores {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		if scores[ids[i]] == scores[ids[j]] {
			return ids[i] < ids[j]
		}
		return scores[ids[i]] > scores[ids[j]]
	})
	return ids
}
