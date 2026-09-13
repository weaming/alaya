package main

import (
	"strings"
	"testing"
)

// scope 是 Alaya 的一等轴：同一条断言在不同 scope 下是不同的声称。
// 读写两条路径都要能按它过滤，否则「个人记忆」与「团队共识」会混作一谈。
func TestCLIScopeFiltering(t *testing.T) {
	home := t.TempDir()

	// 同一主体、同一谓词，分别落在两个 scope
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "香港", "--scope", "user:garden")
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "东京", "--scope", "team:alpha")

	t.Run("claim 写入指定 scope", func(t *testing.T) {
		out := runCLI(t, home, "stats")
		if !strings.Contains(out, "事件总数: 2") || !strings.Contains(out, "scope 数: 2") {
			t.Fatalf("两条应落在不同 scope，实际:\n%s", out)
		}
	})

	t.Run("stats 按 scope 过滤", func(t *testing.T) {
		out := runCLI(t, home, "stats", "--scope", "team:alpha")
		if !strings.Contains(out, "事件总数: 1") {
			t.Fatalf("team:alpha 应只有 1 条，实际:\n%s", out)
		}
		if !strings.Contains(out, "scope 数: 1") {
			t.Fatalf("过滤后应只看到一个 scope，实际:\n%s", out)
		}
	})

	t.Run("search 按 scope 过滤", func(t *testing.T) {
		out := runCLI(t, home, "search", "lives_in", "--scope", "team:alpha")

		if !strings.Contains(out, "东京") {
			t.Fatalf("应命中 team:alpha 的记录，实际:\n%s", out)
		}
		if strings.Contains(out, "香港") {
			t.Fatalf("不应命中其它 scope 的记录，实际:\n%s", out)
		}
	})

	t.Run("history 按 scope 过滤", func(t *testing.T) {
		out := runCLI(t, home, "history", "--scope", "team:alpha", testSubject)

		if !strings.Contains(out, "东京") {
			t.Fatalf("应返回 team:alpha 的演变，实际:\n%s", out)
		}
		if strings.Contains(out, "香港") {
			t.Fatalf("不应返回其它 scope 的记录，实际:\n%s", out)
		}
	})

	t.Run("不传 scope 时不过滤", func(t *testing.T) {
		out := runCLI(t, home, "search", "lives_in")

		if !strings.Contains(out, "东京") || !strings.Contains(out, "香港") {
			t.Fatalf("缺省应看到全部 scope，实际:\n%s", out)
		}
	})
}

// 按 world 过滤同理——虚构世界与现实的断言不应互相污染。
func TestCLIWorldFiltering(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "香港", "--world", "world:real")
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "冬木市", "--world", "world:fate")

	out := runCLI(t, home, "search", "lives_in", "--world", "world:fate")

	if !strings.Contains(out, "冬木市") {
		t.Fatalf("应命中虚构世界的记录，实际:\n%s", out)
	}
	if strings.Contains(out, "香港") {
		t.Fatalf("不应命中现实世界的记录，实际:\n%s", out)
	}
}

// 标志与位置参数可以任意顺序混写。用户自然会写 `search 关键词 --scope X`，
// 而 Go 的 flag 包遇到第一个位置参数就停止解析，会把 --scope 当成位置参数——
// 报错还看不出来，因为搜索词照样能匹配上。
func TestCLIArgumentOrderDoesNotMatter(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "香港", "--scope", "user:garden")
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "东京", "--scope", "team:alpha")

	tests := []struct {
		name string
		args []string
	}{
		{"标志在前", []string{"search", "--scope", "team:alpha", "lives_in"}},
		{"位置参数在前", []string{"search", "lives_in", "--scope", "team:alpha"}},
		{"等号形式", []string{"search", "--scope=team:alpha", "lives_in"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			out := runCLI(t, home, test.args...)

			if !strings.Contains(out, "东京") {
				t.Fatalf("应命中 team:alpha，实际:\n%s", out)
			}
			if strings.Contains(out, "香港") {
				t.Fatalf("过滤未生效——不应命中其它 scope，实际:\n%s", out)
			}
		})
	}
}

func TestCLIHistoryArgumentOrderDoesNotMatter(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "香港", "--scope", "user:garden")
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "东京", "--scope", "team:alpha")

	for _, args := range [][]string{
		{"history", "--scope", "team:alpha", testSubject},
		{"history", testSubject, "--scope", "team:alpha"},
	} {
		out := runCLI(t, home, args...)

		if !strings.Contains(out, "东京") || strings.Contains(out, "香港") {
			t.Fatalf("args=%v 的过滤未生效，实际:\n%s", args, out)
		}
	}
}

// 只按 scope 过滤应可用——「看看这个 scope 里都有什么」是自然需求，
// 不该强制先给出主体才肯列。
func TestCLIHistoryByScopeOnly(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in",
		"--object", "香港", "--scope", "user:garden")
	runCLI(t, home, "claim", "--subject", "urn:alaya:person:other", "--predicate", "likes",
		"--object", "别的东西", "--scope", "team:alpha")

	out := runCLI(t, home, "history", "--scope", "user:garden")

	if !strings.Contains(out, "香港") {
		t.Fatalf("应列出该 scope 的记录，实际:\n%s", out)
	}
	if strings.Contains(out, "别的东西") {
		t.Fatalf("不应列出其它 scope 的记录，实际:\n%s", out)
	}
	if !strings.Contains(out, "user:garden") {
		t.Fatalf("标题应说明看的是什么范围，实际:\n%s", out)
	}
}

// 完全不给条件意味着「把整个库倒出来」，那是调用方该显式做的事，
// 不该由一次疏漏的调用静默完成。
func TestCLIHistoryRequiresSomeFilter(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in", "--object", "香港")

	stderr := runCLIExpectingFailure(t, home, "history")
	if !strings.Contains(stderr, "用法") {
		t.Fatalf("应提示用法，实际: %s", stderr)
	}
}
