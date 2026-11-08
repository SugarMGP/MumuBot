package memory

import (
	"encoding/json"

	"gorm.io/gorm"
)

// HeadEntry 是一次换代要写入的代头行
type HeadEntry struct {
	Kind    string
	Payload json.RawMessage
}

// replaceCheckpointHeads 在摘要事务里更换代头，旧人格不会进入新检查点
func (m *Manager) replaceCheckpointHeads(tx *gorm.DB, kind string, targetID int64, heads []HeadEntry) ([]uint, TranscriptCandidates, error) {
	released, err := m.DeleteTranscriptHeads(tx, kind, targetID)
	if err != nil {
		return nil, TranscriptCandidates{}, err
	}
	ids := make([]uint, 0, len(heads))
	for _, head := range heads {
		entry := &TranscriptEntry{ConversationKind: kind, TargetID: targetID, EntryKind: head.Kind, Payload: head.Payload}
		if err := tx.Create(entry).Error; err != nil {
			return nil, TranscriptCandidates{}, err
		}
		ids = append(ids, entry.ID)
	}
	return ids, released, nil
}
