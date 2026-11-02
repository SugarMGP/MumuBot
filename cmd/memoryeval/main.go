// memoryeval 用真实日志人工标注的小评测集比较三种召回：
// exact（原词精确）、fuzzy（原词相似度）、expanded（模型给的同义词/别名，or）。
//
// 用法：
//
//	MUMU_DATABASE_DSN=postgres://... go run ./cmd/memoryeval -cases cases.jsonl
//
// cases.jsonl 每行一个 JSON：
//
//	{"id":"q1","query":"上次说的显卡","keywords":["显卡","GPU","4090"],"match":"or","expected_ids":[12]}
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"mumu-bot/internal/memory"
)

type evalCase struct {
	ID          string   `json:"id"`
	Query       string   `json:"query"`
	Keywords    []string `json:"keywords"`
	Match       string   `json:"match"`
	ExpectedIDs []uint   `json:"expected_ids"`
}

type stats struct {
	name   string
	total  int
	hit1   int
	hitK   int
	misses []string
}

func main() {
	casesPath := flag.String("cases", "", "JSONL 评测集路径")
	dsn := flag.String("dsn", os.Getenv("MUMU_DATABASE_DSN"), "PostgreSQL DSN（默认读 MUMU_DATABASE_DSN）")
	limit := flag.Int("limit", 5, "每条 query 取前多少个结果算命中")
	verbose := flag.Bool("v", false, "打印每条 case 的命中情况")
	flag.Parse()

	if strings.TrimSpace(*casesPath) == "" {
		fail("-cases 必填")
	}
	if strings.TrimSpace(*dsn) == "" {
		fail("缺少 DSN：用 -dsn 或 MUMU_DATABASE_DSN")
	}
	cases, err := loadCases(*casesPath)
	if err != nil {
		fail(err.Error())
	}
	db, err := memory.OpenDB(*dsn)
	if err != nil {
		fail(err.Error())
	}
	mgr, err := memory.NewManager(db, "")
	if err != nil {
		fail(err.Error())
	}

	modes := []stats{{name: "exact"}, {name: "fuzzy"}, {name: "expanded"}}
	for _, c := range cases {
		if strings.TrimSpace(c.Query) == "" || len(c.ExpectedIDs) == 0 {
			fail(fmt.Sprintf("case %s 缺少 query 或 expected_ids", c.ID))
		}
		for i := range modes {
			ids, err := search(mgr, c, modes[i].name, *limit)
			if err != nil {
				fail(fmt.Sprintf("case %s %s: %v", c.ID, modes[i].name, err))
			}
			modes[i].total++
			hitAt := -1
			for pos, id := range ids {
				if containsID(c.ExpectedIDs, id) {
					hitAt = pos
					break
				}
			}
			if hitAt == 0 {
				modes[i].hit1++
			}
			if hitAt >= 0 {
				modes[i].hitK++
			} else {
				modes[i].misses = append(modes[i].misses, c.ID)
			}
			if *verbose {
				fmt.Printf("%-8s %-16s hit=%d ids=%v\n", modes[i].name, c.ID, hitAt, ids)
			}
		}
	}

	fmt.Printf("\n%-9s %6s %8s %8s   %s\n", "mode", "cases", "hit@1", "hit@K", "misses")
	for _, m := range modes {
		fmt.Printf("%-9s %6d %7d%% %7d%%   %s\n", m.name, m.total,
			percent(m.hit1, m.total), percent(m.hitK, m.total), strings.Join(m.misses, ","))
	}
}

func search(mgr *memory.Manager, c evalCase, mode string, limit int) ([]uint, error) {
	keywords := []string{c.Query}
	queryMode := "exact"
	switch mode {
	case "fuzzy":
		queryMode = "fuzzy"
	case "expanded":
		if len(c.Keywords) > 0 {
			keywords = c.Keywords
		}
	}
	match := c.Match
	if strings.TrimSpace(match) == "" {
		match = "or"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	page, err := mgr.SearchMemories(ctx, memory.MemorySearchQuery{
		Keywords: keywords, Match: match, Mode: queryMode, Limit: limit,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.ID)
	}
	return ids, nil
}

func loadCases(path string) ([]evalCase, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var cases []evalCase
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for line := 1; scanner.Scan(); line++ {
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		var c evalCase
		if err := json.Unmarshal([]byte(raw), &c); err != nil {
			return nil, fmt.Errorf("第 %d 行解析失败: %w", line, err)
		}
		cases = append(cases, c)
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if len(cases) == 0 {
		return nil, fmt.Errorf("评测集为空")
	}
	return cases, nil
}

func containsID(ids []uint, target uint) bool {
	for _, id := range ids {
		if id == target {
			return true
		}
	}
	return false
}

func percent(part, total int) int {
	if total == 0 {
		return 0
	}
	return part * 100 / total
}

func fail(message string) {
	fmt.Fprintln(os.Stderr, message)
	os.Exit(1)
}
