package search

import (
	"reflect"
	"testing"

	"github.com/weaming/alaya/models"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"英文按词切分", "hand drip", []string{"hand", "drip"}},
		{"大小写归一", "Hand Drip", []string{"hand", "drip"}},
		{"中文按 bigram", "喜欢咖啡", []string{"喜欢", "欢咖", "咖啡"}},
		{"单个汉字保留", "茶", []string{"茶"}},
		{"中英混排", "喜欢 coffee", []string{"喜欢", "coffee"}},
		{"标点作为分隔", "hand-drip, please", []string{"hand", "drip", "please"}},
		{"空字符串", "", []string{}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := tokenize(test.input)
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("tokenize(%q) = %v，期望 %v", test.input, got, test.want)
			}
		})
	}
}

func TestSearchRanksRelevantFirst(t *testing.T) {
	index := New()
	index.Build([]models.Event{
		{Subject: "garden", Predicate: "prefers", Object: "手冲咖啡"},
		{Subject: "garden", Predicate: "likes", Object: "机械键盘"},
		{Subject: "garden", Predicate: "reads", Object: "科幻小说"},
	})

	hits := index.Search("咖啡", 10)
	if len(hits) == 0 {
		t.Fatal("中文查询应命中")
	}
	if hits[0].Event.Object != "手冲咖啡" {
		t.Fatalf("首选结果应为手冲咖啡，实际 %q", hits[0].Event.Object)
	}
}

func TestSearchMatchesEnglish(t *testing.T) {
	index := New()
	index.Build([]models.Event{
		{Subject: "garden", Predicate: "uses", Object: "neovim"},
		{Subject: "garden", Predicate: "uses", Object: "vscode"},
	})

	hits := index.Search("neovim", 10)
	if len(hits) != 1 {
		t.Fatalf("应恰好命中 1 条，实际 %d", len(hits))
	}
	if hits[0].Event.Object != "neovim" {
		t.Fatalf("命中了错误条目: %q", hits[0].Event.Object)
	}
}

func TestSearchRespectsLimit(t *testing.T) {
	index := New()
	for i := 0; i < 20; i++ {
		index.Add(models.Event{Subject: "s", Predicate: "p", Object: "共同关键词"})
	}

	if got := len(index.Search("关键词", 5)); got != 5 {
		t.Fatalf("limit=5 应返回 5 条，实际 %d", got)
	}
}

func TestSearchEmptyQueryReturnsNothing(t *testing.T) {
	index := New()
	index.Add(models.Event{Subject: "s", Predicate: "p", Object: "o"})

	if hits := index.Search("   ", 10); len(hits) != 0 {
		t.Fatalf("空查询应无结果，实际 %d 条", len(hits))
	}
}
