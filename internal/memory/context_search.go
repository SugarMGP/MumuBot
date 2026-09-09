package memory

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
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

func uintIDArray(values []uint) pgtype.Array[int64] {
	elements := make([]int64, len(values))
	for i, value := range values {
		elements[i] = int64(value)
	}
	return int64Array(elements)
}
