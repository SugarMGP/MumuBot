package agent

import (
	"crypto/rand"
	"encoding/hex"

	"mumu-bot/internal/memory"
)

// forwardDraft 一份待落库的合并转发，父级排在嵌套之前
type forwardDraft struct {
	Handle string
	Parent string
	Nodes  []memory.ForwardNode
}

// newForwardHandle 生成一个全局唯一的转发句柄
func newForwardHandle() string {
	var buf [6]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return ""
	}
	return "f" + hex.EncodeToString(buf[:])
}
