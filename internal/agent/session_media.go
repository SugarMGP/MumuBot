package agent

import (
	"strings"

	"mumu-bot/internal/media"
)

// mediaShortHashLen 会话内用内容 md5 的前若干位定位媒体
const mediaShortHashLen = 8

// mediaIndex 会话内的媒体索引：同一内容复用同一个短哈希，不同内容撞前缀时逐位加长
type mediaIndex struct {
	byShort map[string]media.Ref
	byFull  map[string]string
}

// newMediaIndex 创建空索引
func newMediaIndex() *mediaIndex {
	return &mediaIndex{byShort: map[string]media.Ref{}, byFull: map[string]string{}}
}

// shortHash 登记一份媒体并返回会话内短哈希，调用方需已持会话锁
func (idx *mediaIndex) shortHash(ref media.Ref) string {
	if ref.Hash == "" {
		return ""
	}
	if short, ok := idx.byFull[ref.Hash]; ok {
		// 同一内容再次出现时刷新引用，让收藏状态等标记保持最新
		idx.byShort[short] = ref
		return short
	}
	for length := mediaShortHashLen; length <= len(ref.Hash); length++ {
		candidate := ref.Hash[:length]
		if _, taken := idx.byShort[candidate]; taken {
			continue
		}
		idx.byShort[candidate] = ref
		idx.byFull[ref.Hash] = candidate
		return candidate
	}
	return ""
}

// placeholders 渲染媒体占位，图片与表情分开，已收藏的表情带标记
func (idx *mediaIndex) placeholders(refs []media.Ref) string {
	parts := make([]string, 0, len(refs))
	for _, ref := range refs {
		short := idx.shortHash(ref)
		if short == "" {
			continue
		}
		if !ref.Sticker {
			parts = append(parts, "[图片:"+short+"]")
			continue
		}
		if ref.Collected {
			parts = append(parts, "[表情包:"+short+"(已收藏)]")
			continue
		}
		parts = append(parts, "[表情包:"+short+"]")
	}
	return strings.Join(parts, " ")
}

// resolve 按会话内短哈希取媒体
func (idx *mediaIndex) resolve(hash string) (media.Ref, bool) {
	ref, ok := idx.byShort[strings.TrimSpace(hash)]
	return ref, ok
}
