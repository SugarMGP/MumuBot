package vector

import (
	"context"
	"fmt"
	"io"

	"github.com/milvus-io/milvus/client/v2/column"
	"github.com/milvus-io/milvus/client/v2/entity"
	"github.com/milvus-io/milvus/client/v2/index"
	"github.com/milvus-io/milvus/client/v2/milvusclient"
	"go.uber.org/zap"
)

const (
	collectionV2Suffix = "_v2"
	migrationBatchSize = 500
)

func (c *MilvusClient) ensureV2Collection(ctx context.Context) error {
	logicalName := c.collectionName
	targetName := logicalName + collectionV2Suffix

	physicalNames, aliases, err := c.collectionState(ctx)
	if err != nil {
		return err
	}
	if aliasTarget, ok := aliases[logicalName]; ok {
		if aliasTarget != targetName {
			return fmt.Errorf("Milvus 集合别名 %s 指向 %s，预期指向 %s", logicalName, aliasTarget, targetName)
		}
		if err := c.prepareV2Collection(ctx, targetName); err != nil {
			return err
		}
		return nil
	}

	logicalExists := physicalNames[logicalName]
	if logicalExists {
		if err := c.validateOldCollection(ctx, logicalName); err != nil {
			return err
		}
	}

	if !physicalNames[targetName] {
		if err := c.createV2Collection(ctx, targetName); err != nil {
			return err
		}
	} else if err := c.validateV2Collection(ctx, targetName); err != nil {
		return err
	}

	if !logicalExists {
		if err := c.prepareV2Collection(ctx, targetName); err != nil {
			return err
		}
		if err := c.client.CreateAlias(ctx, milvusclient.NewCreateAliasOption(targetName, logicalName)); err != nil {
			return fmt.Errorf("为 Milvus 集合 %s 创建别名 %s 失败: %w", targetName, logicalName, err)
		}
		zap.L().Info("Milvus V2 集合已初始化", zap.String("collection", targetName), zap.String("alias", logicalName))
		return nil
	}

	if err := c.loadCollection(ctx, logicalName); err != nil {
		return err
	}
	latestIDs, err := c.latestOldIDs(ctx, logicalName)
	if err != nil {
		return err
	}
	zap.L().Info("开始迁移 Milvus 集合",
		zap.String("source", logicalName),
		zap.String("target", targetName),
		zap.Int("vectors", len(latestIDs)))
	migrated, err := c.copyOldVectors(ctx, logicalName, targetName, latestIDs)
	if err != nil {
		return err
	}
	if migrated > 0 {
		flushTask, err := c.client.Flush(ctx, milvusclient.NewFlushOption(targetName))
		if err != nil {
			return fmt.Errorf("刷新 Milvus 迁移集合 %s 失败: %w", targetName, err)
		}
		if err := flushTask.Await(ctx); err != nil {
			return fmt.Errorf("等待 Milvus 迁移集合 %s 刷新完成失败: %w", targetName, err)
		}
	}
	if err := c.prepareV2Collection(ctx, targetName); err != nil {
		return err
	}
	if err := c.validateMigratedIDs(ctx, targetName, latestIDs); err != nil {
		return err
	}

	if err := c.client.ReleaseCollection(ctx, milvusclient.NewReleaseCollectionOption(logicalName)); err != nil {
		return fmt.Errorf("释放旧 Milvus 集合 %s 失败: %w", logicalName, err)
	}
	if err := c.client.DropCollection(ctx, milvusclient.NewDropCollectionOption(logicalName)); err != nil {
		return fmt.Errorf("删除旧 Milvus 集合 %s 失败: %w", logicalName, err)
	}
	if err := c.client.CreateAlias(ctx, milvusclient.NewCreateAliasOption(targetName, logicalName)); err != nil {
		return fmt.Errorf("为 Milvus 集合 %s 创建别名 %s 失败: %w", targetName, logicalName, err)
	}
	zap.L().Info("Milvus 集合迁移完成",
		zap.String("deleted_source", logicalName),
		zap.String("target", targetName),
		zap.String("alias", logicalName),
		zap.Int("vectors", migrated))
	return nil
}

func (c *MilvusClient) collectionState(ctx context.Context) (map[string]bool, map[string]string, error) {
	names, err := c.client.ListCollections(ctx, milvusclient.NewListCollectionOption())
	if err != nil {
		return nil, nil, fmt.Errorf("列出 Milvus 集合失败: %w", err)
	}
	physicalNames := make(map[string]bool, len(names))
	aliases := make(map[string]string)
	for _, name := range names {
		physicalNames[name] = true
		collectionAliases, err := c.client.ListAliases(ctx, milvusclient.NewListAliasesOption(name))
		if err != nil {
			return nil, nil, fmt.Errorf("列出 Milvus 集合 %s 的别名失败: %w", name, err)
		}
		for _, alias := range collectionAliases {
			if existingTarget, exists := aliases[alias]; exists && existingTarget != name {
				return nil, nil, fmt.Errorf("Milvus 别名 %s 同时指向 %s 和 %s", alias, existingTarget, name)
			}
			aliases[alias] = name
		}
	}
	return physicalNames, aliases, nil
}

func (c *MilvusClient) createV2Collection(ctx context.Context, collectionName string) error {
	schema := entity.NewSchema().
		WithName(collectionName).
		WithDescription("Mumu bot vectors").
		WithField(entity.NewField().WithName("id").WithDataType(entity.FieldTypeInt64).WithIsPrimaryKey(true).WithIsAutoID(false)).
		WithField(entity.NewField().WithName("group_id").WithDataType(entity.FieldTypeInt64)).
		WithField(entity.NewField().WithName("mem_type").WithDataType(entity.FieldTypeVarChar).WithMaxLength(64)).
		WithField(entity.NewField().WithName("embedding").WithDataType(entity.FieldTypeFloatVector).WithDim(int64(c.cfg.VectorDim)))
	if err := c.client.CreateCollection(ctx, milvusclient.NewCreateCollectionOption(collectionName, schema)); err != nil {
		return fmt.Errorf("创建 Milvus V2 集合 %s 失败: %w", collectionName, err)
	}
	return nil
}

func (c *MilvusClient) prepareV2Collection(ctx context.Context, collectionName string) error {
	if err := c.validateV2Collection(ctx, collectionName); err != nil {
		return err
	}
	indexes, err := c.client.ListIndexes(ctx, milvusclient.NewListIndexOption(collectionName).WithFieldName("embedding"))
	if err != nil {
		return fmt.Errorf("列出 Milvus 集合 %s 的索引失败: %w", collectionName, err)
	}
	if len(indexes) == 0 {
		metricType := entity.COSINE
		switch c.cfg.MetricType {
		case "IP":
			metricType = entity.IP
		case "L2":
			metricType = entity.L2
		}
		task, err := c.client.CreateIndex(ctx, milvusclient.NewCreateIndexOption(
			collectionName, "embedding", index.NewHNSWIndex(metricType, 16, 256)))
		if err != nil {
			return fmt.Errorf("为 Milvus 集合 %s 创建索引失败: %w", collectionName, err)
		}
		if err := task.Await(ctx); err != nil {
			return fmt.Errorf("等待 Milvus 集合 %s 索引创建完成失败: %w", collectionName, err)
		}
	}
	return c.loadCollection(ctx, collectionName)
}

func (c *MilvusClient) validateOldCollection(ctx context.Context, collectionName string) error {
	collection, err := c.client.DescribeCollection(ctx, milvusclient.NewDescribeCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("读取旧 Milvus 集合 %s 结构失败: %w", collectionName, err)
	}
	fields := fieldsByName(collection.Schema)
	if _, ok := fields["memory_id"]; !ok {
		return fmt.Errorf("Milvus 集合 %s 不含 memory_id，无法识别为旧集合", collectionName)
	}
	expectedTypes := map[string]entity.FieldType{
		"id": entity.FieldTypeInt64, "memory_id": entity.FieldTypeInt64,
		"group_id": entity.FieldTypeInt64, "mem_type": entity.FieldTypeVarChar,
		"embedding": entity.FieldTypeFloatVector,
	}
	for name, dataType := range expectedTypes {
		field, ok := fields[name]
		if !ok || field.DataType != dataType {
			return fmt.Errorf("旧 Milvus 集合 %s 的字段 %s 缺失或类型不匹配", collectionName, name)
		}
	}
	dim, err := fields["embedding"].GetDim()
	if err != nil || dim != int64(c.cfg.VectorDim) {
		return fmt.Errorf("旧 Milvus 集合 %s 的向量维度不匹配: %d", collectionName, dim)
	}
	return nil
}

func (c *MilvusClient) validateV2Collection(ctx context.Context, collectionName string) error {
	collection, err := c.client.DescribeCollection(ctx, milvusclient.NewDescribeCollectionOption(collectionName))
	if err != nil {
		return fmt.Errorf("读取 Milvus V2 集合 %s 结构失败: %w", collectionName, err)
	}
	fields := fieldsByName(collection.Schema)
	if _, exists := fields["memory_id"]; exists {
		return fmt.Errorf("Milvus V2 集合 %s 仍包含 memory_id 字段", collectionName)
	}
	if len(fields) != 4 {
		return fmt.Errorf("Milvus V2 集合 %s 字段数量异常: %d", collectionName, len(fields))
	}
	id := fields["id"]
	if id == nil || id.DataType != entity.FieldTypeInt64 || !id.PrimaryKey || id.AutoID {
		return fmt.Errorf("Milvus V2 集合 %s 的 id 必须是非 AutoID 的 Int64 主键", collectionName)
	}
	if field := fields["group_id"]; field == nil || field.DataType != entity.FieldTypeInt64 {
		return fmt.Errorf("Milvus V2 集合 %s 的 group_id 字段不匹配", collectionName)
	}
	if field := fields["mem_type"]; field == nil || field.DataType != entity.FieldTypeVarChar || field.TypeParams[entity.TypeParamMaxLength] != "64" {
		return fmt.Errorf("Milvus V2 集合 %s 的 mem_type 字段不匹配", collectionName)
	}
	embedding := fields["embedding"]
	if embedding == nil || embedding.DataType != entity.FieldTypeFloatVector {
		return fmt.Errorf("Milvus V2 集合 %s 的 embedding 字段不匹配", collectionName)
	}
	dim, err := embedding.GetDim()
	if err != nil || dim != int64(c.cfg.VectorDim) {
		return fmt.Errorf("Milvus V2 集合 %s 的向量维度不匹配: %d", collectionName, dim)
	}
	return nil
}

func fieldsByName(schema *entity.Schema) map[string]*entity.Field {
	fields := make(map[string]*entity.Field, len(schema.Fields))
	for _, field := range schema.Fields {
		fields[field.Name] = field
	}
	return fields
}

func (c *MilvusClient) latestOldIDs(ctx context.Context, sourceName string) (map[int64]int64, error) {
	iterator, err := c.client.QueryIterator(ctx, milvusclient.NewQueryIteratorOption(sourceName).
		WithBatchSize(migrationBatchSize).
		WithFilter("").
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "memory_id"))
	if err != nil {
		return nil, fmt.Errorf("创建旧 Milvus 集合 %s 扫描失败: %w", sourceName, err)
	}
	latestIDs := make(map[int64]int64)
	for {
		batch, err := iterator.Next(ctx)
		if err == io.EOF {
			return latestIDs, nil
		}
		if err != nil {
			return nil, fmt.Errorf("扫描旧 Milvus 集合 %s 失败: %w", sourceName, err)
		}
		ids, memoryIDs, err := oldIDColumns(batch)
		if err != nil {
			return nil, fmt.Errorf("读取旧 Milvus 集合 %s 失败: %w", sourceName, err)
		}
		for i, id := range ids {
			memoryID := memoryIDs[i]
			if memoryID < 0 {
				return nil, fmt.Errorf("旧 Milvus 集合 %s 包含负 memory_id: %d", sourceName, memoryID)
			}
			if current, ok := latestIDs[memoryID]; !ok || id > current {
				latestIDs[memoryID] = id
			}
		}
	}
}

func oldIDColumns(batch milvusclient.ResultSet) ([]int64, []int64, error) {
	idColumn, ok := batch.GetColumn("id").(*column.ColumnInt64)
	if !ok {
		return nil, nil, fmt.Errorf("缺少 int64 id 字段")
	}
	memoryIDColumn, ok := batch.GetColumn("memory_id").(*column.ColumnInt64)
	if !ok {
		return nil, nil, fmt.Errorf("缺少 int64 memory_id 字段")
	}
	if idColumn.Len() != memoryIDColumn.Len() {
		return nil, nil, fmt.Errorf("id 与 memory_id 字段长度不一致")
	}
	return idColumn.Data(), memoryIDColumn.Data(), nil
}

func (c *MilvusClient) copyOldVectors(ctx context.Context, sourceName, targetName string, latestIDs map[int64]int64) (int, error) {
	iterator, err := c.client.QueryIterator(ctx, milvusclient.NewQueryIteratorOption(sourceName).
		WithBatchSize(migrationBatchSize).
		WithFilter("").
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id", "memory_id", "group_id", "mem_type", "embedding"))
	if err != nil {
		return 0, fmt.Errorf("创建旧 Milvus 集合 %s 数据迁移扫描失败: %w", sourceName, err)
	}
	total := 0
	for {
		batch, err := iterator.Next(ctx)
		if err == io.EOF {
			return total, nil
		}
		if err != nil {
			return 0, fmt.Errorf("迁移扫描旧 Milvus 集合 %s 失败: %w", sourceName, err)
		}
		ids, memoryIDs, err := oldIDColumns(batch)
		if err != nil {
			return 0, err
		}
		groupIDs, ok := batch.GetColumn("group_id").(*column.ColumnInt64)
		if !ok || groupIDs.Len() != len(ids) {
			return 0, fmt.Errorf("旧 Milvus 集合 %s 的 group_id 字段无效", sourceName)
		}
		memTypes, ok := batch.GetColumn("mem_type").(*column.ColumnVarChar)
		if !ok || memTypes.Len() != len(ids) {
			return 0, fmt.Errorf("旧 Milvus 集合 %s 的 mem_type 字段无效", sourceName)
		}
		embeddings, ok := batch.GetColumn("embedding").(*column.ColumnFloatVector)
		if !ok || embeddings.Len() != len(ids) || embeddings.Dim() != c.cfg.VectorDim {
			return 0, fmt.Errorf("旧 Milvus 集合 %s 的 embedding 字段无效", sourceName)
		}

		newIDs := make([]int64, 0, len(ids))
		newGroupIDs := make([]int64, 0, len(ids))
		newMemTypes := make([]string, 0, len(ids))
		newEmbeddings := make([][]float32, 0, len(ids))
		for i, oldID := range ids {
			memoryID := memoryIDs[i]
			if latestIDs[memoryID] != oldID {
				continue
			}
			newIDs = append(newIDs, memoryID)
			newGroupIDs = append(newGroupIDs, groupIDs.Data()[i])
			newMemTypes = append(newMemTypes, memTypes.Data()[i])
			newEmbeddings = append(newEmbeddings, []float32(embeddings.Data()[i]))
		}
		if len(newIDs) == 0 {
			continue
		}
		result, err := c.client.Upsert(ctx, milvusclient.NewColumnBasedInsertOption(targetName,
			column.NewColumnInt64("id", newIDs),
			column.NewColumnInt64("group_id", newGroupIDs),
			column.NewColumnVarChar("mem_type", newMemTypes),
			column.NewColumnFloatVector("embedding", c.cfg.VectorDim, newEmbeddings)))
		if err != nil {
			return 0, fmt.Errorf("写入 Milvus V2 集合 %s 失败: %w", targetName, err)
		}
		if result.UpsertCount != int64(len(newIDs)) {
			return 0, fmt.Errorf("写入 Milvus V2 集合 %s 数量异常: want=%d got=%d", targetName, len(newIDs), result.UpsertCount)
		}
		total += len(newIDs)
	}
}

func (c *MilvusClient) validateMigratedIDs(ctx context.Context, targetName string, expected map[int64]int64) error {
	iterator, err := c.client.QueryIterator(ctx, milvusclient.NewQueryIteratorOption(targetName).
		WithBatchSize(migrationBatchSize).
		WithFilter("").
		WithConsistencyLevel(entity.ClStrong).
		WithOutputFields("id"))
	if err != nil {
		return fmt.Errorf("创建 Milvus V2 集合 %s 校验扫描失败: %w", targetName, err)
	}
	actualCount := 0
	for {
		batch, err := iterator.Next(ctx)
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("扫描 Milvus V2 集合 %s 失败: %w", targetName, err)
		}
		ids, ok := batch.GetColumn("id").(*column.ColumnInt64)
		if !ok {
			return fmt.Errorf("Milvus V2 集合 %s 缺少 int64 id 字段", targetName)
		}
		for _, id := range ids.Data() {
			if _, ok := expected[id]; !ok {
				return fmt.Errorf("Milvus V2 集合 %s 包含源集合中不存在的 id: %d", targetName, id)
			}
			actualCount++
		}
	}
	if actualCount != len(expected) {
		return fmt.Errorf("Milvus V2 集合 %s 迁移数量校验失败: want=%d got=%d", targetName, len(expected), actualCount)
	}
	return nil
}
