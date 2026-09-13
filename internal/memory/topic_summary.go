package memory

import (
	"context"
	"slices"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// TopicSummaryTextSQL 只索引摘要语义字段，不把字段名和消息编号当成原文
const TopicSummaryTextSQL = `concat_ws(' ',ts.summary_json->>'title',ts.summary_json->>'gist',ts.summary_json->>'open_loops',ts.summary_json->>'recent_turns',ts.summary_json->>'keywords')`

// TopicSummaryValiditySQL 在选定版本之后检查来源，不能借失效过滤回退到旧解释
const TopicSummaryValiditySQL = `(COALESCE(ts.summary_json->>'version','1')<>'2' OR EXISTS(SELECT 1 FROM topic_summary_sources ss WHERE ss.summary_id=ts.id))
 AND NOT EXISTS(SELECT 1 FROM topic_summary_sources ss JOIN message_logs ml ON ml.id=ss.message_log_id WHERE ss.summary_id=ts.id AND (ml.recalled_at IS NOT NULL OR btrim(ml.text_content)=''))`

// LatestTopicSummaries 返回固定原文上界内各话题的最新版本，包括来源失效的版本
func LatestTopicSummaries(db *gorm.DB, groupID int64, upper uint) *gorm.DB {
	q := db.Table("topic_summaries ts").Select("DISTINCT ON (ta.topic_id) ts.*, ta.topic_id").
		Joins("JOIN topic_assignments ta ON ta.id=ts.through_topic_assignment_id JOIN topic_threads tt ON tt.id=ta.topic_id")
	if groupID > 0 {
		q = q.Where("tt.group_id=?", groupID)
	}
	if upper > 0 {
		q = q.Where(`NOT EXISTS(SELECT 1 FROM topic_assignments a WHERE a.topic_id=ta.topic_id AND a.id<=ts.through_topic_assignment_id AND a.message_log_id>?)`, upper).
			Where(`NOT EXISTS(SELECT 1 FROM topic_summary_sources ss WHERE ss.summary_id=ts.id AND ss.message_log_id>?)`, upper)
	}
	latest := q.Order("ta.topic_id,ts.id DESC")
	return db.Table("(?) AS ts", latest).Select("ts.*, (" + TopicSummaryValiditySQL + ") AS sources_valid")
}

func saveTopicSources(ctx context.Context, tx *gorm.DB, batch KnowledgeBatch, topicID uint, input ConversationTopic, record *TopicSummaryRecord) error {
	ids := slices.Clone(input.SourceMessageIDs)
	if len(ids) == 0 {
		return invalidKnowledge("话题摘要必须提供 source_message_ids，请读取支持摘要的完整原文")
	}
	seenTopics := map[uint]bool{}
	for _, related := range input.Summary.RelatedTopics {
		if related.TopicID == 0 || related.TopicID == topicID || seenTopics[related.TopicID] || strings.TrimSpace(related.Reason) == "" || len(related.SourceMessageIDs) == 0 {
			return invalidKnowledge("关联话题必须不同、去重，并提供关联说明和原文来源")
		}
		seenTopics[related.TopicID] = true
		var count int64
		if err := tx.Table("topic_threads tt").Where("tt.id=? AND tt.group_id=?", related.TopicID, batch.GroupID).
			Where("EXISTS(SELECT 1 FROM topic_assignments a WHERE a.topic_id=tt.id AND a.message_log_id<=?)", batch.ThroughID).Count(&count).Error; err != nil {
			return err
		}
		if count != 1 {
			return invalidKnowledge("关联话题 %d 不在本群固定范围内", related.TopicID)
		}
		ids = append(ids, related.SourceMessageIDs...)
	}
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if !slices.Contains(batch.ReadMessageIDs, id) {
			return invalidKnowledge("摘要原文 %d 尚未完整读取，请先使用 readContext", id)
		}
	}
	var rows []MessageLog
	if err := tx.WithContext(ctx).Clauses(clause.Locking{Strength: "UPDATE"}).Where("group_id=? AND id IN ? AND id<=? AND recalled_at IS NULL AND btrim(text_content)<>''", batch.GroupID, ids, batch.ThroughID).Order("id").Find(&rows).Error; err != nil {
		return err
	}
	if len(rows) != len(ids) {
		return ErrSnapshotChanged
	}
	sources := make([]TopicSummarySource, 0, len(ids))
	for _, id := range ids {
		sources = append(sources, TopicSummarySource{SummaryID: record.ID, MessageLogID: id})
	}
	return tx.Create(&sources).Error
}
