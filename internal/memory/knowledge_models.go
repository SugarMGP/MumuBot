package memory

import (
	"time"

	pgvector "github.com/pgvector/pgvector-go"
)

type KnowledgeItem struct {
	ID                uint             `gorm:"primaryKey" json:"id"`
	GroupID           int64            `json:"group_id"`
	SubjectUserID     int64            `json:"subject_user_id"`
	Kind              string           `json:"kind"`
	Label             string           `json:"label"`
	Content           string           `json:"content"`
	Status            string           `json:"status"`
	Embedding         *pgvector.Vector `gorm:"type:vector" json:"-"`
	ReviewedThroughID uint             `json:"reviewed_through_id"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
}

func (KnowledgeItem) TableName() string { return "knowledge_items" }

type KnowledgeRelation struct {
	ID           uint   `gorm:"primaryKey" json:"id"`
	SourceItemID uint   `json:"source_item_id"`
	TargetItemID uint   `json:"target_item_id"`
	Kind         string `json:"kind"`
	Status       string `json:"status"`
}

func (KnowledgeRelation) TableName() string { return "knowledge_relations" }

type KnowledgeEvidenceSet struct {
	ID         uint  `gorm:"primaryKey" json:"id"`
	ItemID     *uint `json:"item_id,omitempty"`
	RelationID *uint `json:"relation_id,omitempty"`
}

func (KnowledgeEvidenceSet) TableName() string { return "knowledge_evidence_sets" }

type KnowledgeEvidenceMessage struct {
	EvidenceSetID uint `gorm:"primaryKey" json:"evidence_set_id"`
	MessageLogID  uint `gorm:"primaryKey" json:"message_log_id"`
}

func (KnowledgeEvidenceMessage) TableName() string { return "knowledge_evidence_messages" }

type GroupAgentState struct {
	GroupID   int64     `gorm:"primaryKey" json:"group_id"`
	Note      string    `json:"note"`
	UpdatedAt time.Time `json:"updated_at"`
}

func (GroupAgentState) TableName() string { return "group_agent_states" }

type KnowledgeItemInput struct {
	Key           string   `json:"key"`
	ID            uint     `json:"id,omitempty"`
	SubjectUserID int64    `json:"subject_user_id"`
	Kind          string   `json:"kind"`
	Label         string   `json:"label"`
	Content       string   `json:"content"`
	Status        string   `json:"status"`
	EvidenceSets  [][]uint `json:"evidence_sets"`
}
type KnowledgeRelationInput struct {
	SourceKey    string   `json:"source_key"`
	TargetKey    string   `json:"target_key"`
	SourceID     uint     `json:"source_id"`
	TargetID     uint     `json:"target_id"`
	Kind         string   `json:"kind"`
	Status       string   `json:"status"`
	EvidenceSets [][]uint `json:"evidence_sets"`
}
type KnowledgeBatch struct {
	GroupID         int64
	SelfID          int64
	AfterID         uint
	ThroughID       uint
	AdvanceCursor   bool
	RequireAssigned bool
	ReadMessageIDs  []uint
	ExpectedItems   map[uint]time.Time
	Items           []KnowledgeItemInput
	Relations       []KnowledgeRelationInput
	ReviewedIDs     []uint
}
type KnowledgeCommitResult struct {
	ItemIDs     map[string]uint `json:"item_ids"`
	RelationIDs []uint          `json:"relation_ids"`
}
type KnowledgeSearchOptions struct {
	ItemID          uint
	GroupID         int64
	SelfID          int64
	SubjectUserID   *int64
	SubjectIDs      []int64
	Kind            string
	Status          string
	Query           string
	Prepared        *HybridQuery
	IncludeInactive bool
	ThroughID       uint
	Limit           int
	Offset          int
}
type KnowledgeGraph struct {
	Items     []KnowledgeItem     `json:"items"`
	Relations []KnowledgeRelation `json:"relations"`
	HasMore   bool                `json:"has_more"`
}

type KnowledgeGraphOptions struct {
	ThroughID  uint
	SubjectIDs []int64
}
type KnowledgeEvidence struct {
	ID       uint         `json:"id"`
	Messages []MessageLog `json:"messages"`
	Valid    bool         `json:"valid"`
}
type KnowledgeMessageQuery struct {
	GroupID          int64
	ThroughID        uint
	AfterID          uint
	UserID           int64
	Text             string
	ReplyToMessageID *int64
	From             *time.Time
	To               *time.Time
	Limit            int
}
type KnowledgeMessagePage struct {
	Messages []MessageLog `json:"messages"`
	HasMore  bool         `json:"has_more"`
	NextID   uint         `json:"next_id"`
}
