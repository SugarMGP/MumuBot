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
	byFull  map[string]string // 内容 md5 → 短哈希
}

// newMediaIndex 创建空索引
func newMediaIndex() *mediaIndex {
	return &mediaIndex{byShort: map[string]media.Ref{}, byFull: map[string]string{}}
}

// register 登记一份媒体并返回它的短哈希，已经定好的短哈希优先沿用
func (idx *mediaIndex) register(ref media.Ref) string {
	if ref.Hash == "" {
		return ""
	}
	if short, ok := idx.byFull[ref.Hash]; ok {
		// 同一内容再次出现时刷新引用，让收藏状态等标记保持最新
		idx.byShort[short] = ref
		return short
	}
	if ref.Short == "" {
		ref.Short = idx.assign(ref.Hash)
	}
	idx.byShort[ref.Short] = ref
	idx.byFull[ref.Hash] = ref.Short
	return ref.Short
}

// assign 为内容分配短哈希，撞前缀时逐位加长
func (idx *mediaIndex) assign(hash string) string {
	for length := mediaShortHashLen; length <= len(hash); length++ {
		candidate := hash[:length]
		if _, taken := idx.byShort[candidate]; taken {
			continue
		}
		return candidate
	}
	return hash
}

// resolve 按会话内短哈希取媒体
func (idx *mediaIndex) resolve(hash string) (media.Ref, bool) {
	ref, ok := idx.byShort[strings.TrimSpace(hash)]
	return ref, ok
}
