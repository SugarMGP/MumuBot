package agent

import (
	"strings"
	"testing"

	"mumu-bot/internal/memory"
	"mumu-bot/internal/onebot"
)

func TestReplyHeader(t *testing.T) {
	reply := &onebot.ReplyInfo{MessageID: 12345, SenderID: 123, Nickname: "昵称", GroupCard: "群名片", Content: "原文\n 第二行"}
	if got := renderReplyHeader(reply); got != "[回复 群名片(123) 消息 12345: “原文 第二行”] " {
		t.Fatal(got)
	}
	reply.Content = memory.RecalledMessageDisplayContent
	if got := renderReplyHeader(reply); !strings.Contains(got, "已撤回") || strings.Contains(got, "原文") {
		t.Fatal(got)
	}
	reply.Content = strings.Repeat("字", 31)
	if got := renderReplyHeader(reply); !strings.Contains(got, strings.Repeat("字", 29)+"…") {
		t.Fatal(got)
	}
	if got := renderReplyHeader(&onebot.ReplyInfo{MessageID: 7}); strings.Contains(got, "(0)") || !strings.Contains(got, "原文不可用") {
		t.Fatal(got)
	}
	if got := renderReplyHeader(&onebot.ReplyInfo{}); got != "[回复: “原文不可用”] " {
		t.Fatal(got)
	}
}

func TestSociabilityFactor(t *testing.T) {
	for _, test := range []struct{ value, want float64 }{{0.2, 0.5}, {0.33, 0.5}, {0.5, 0.75}, {0.67, 1}, {0.8, 1}} {
		if got := sociabilityFactor(test.value); got != test.want {
			t.Fatalf("%v: %v", test.value, got)
		}
	}
}
