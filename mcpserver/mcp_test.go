package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weaming/alaya/memory"
	"github.com/weaming/alaya/models"
)

const testVersion = "0.0.1"

// newTestSession 建立一对内存传输的 client/server 会话，
// 使测试覆盖真实的协议往返而不只是直接调用 handler。
func newTestSession(t *testing.T) (*mcp.ClientSession, *memory.Memory) {
	t.Helper()

	mem, err := memory.Open(t.TempDir())
	if err != nil {
		t.Fatalf("创建记忆: %v", err)
	}
	t.Cleanup(func() { _ = mem.Close() })

	ctx := context.Background()
	server := New(mem, testVersion)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: testVersion}, nil)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("连接 server: %v", err)
	}
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("连接 client: %v", err)
	}
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession, mem
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("调用 %s: %v", name, err)
	}

	return result
}

func toolText(result *mcp.CallToolResult) string {
	var builder strings.Builder

	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			builder.WriteString(text.Text)
		}
	}

	return builder.String()
}

func TestMCPExposesAllMemoryTools(t *testing.T) {
	session, _ := newTestSession(t)

	result, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("列出工具: %v", err)
	}

	listed := make(map[string]bool, len(result.Tools))
	for _, tool := range result.Tools {
		listed[tool.Name] = true

		// 描述是模型判断"何时该调用"的唯一依据
		if strings.TrimSpace(tool.Description) == "" {
			t.Errorf("工具 %s 缺少描述", tool.Name)
		}
	}

	for _, want := range []string{"memory_claim", "memory_search", "memory_recall", "memory_timeline"} {
		if !listed[want] {
			t.Errorf("未注册工具 %s", want)
		}
	}
}

func TestMCPClaimIsIdempotent(t *testing.T) {
	session, _ := newTestSession(t)

	args := map[string]any{
		"subject":   "urn:alaya:person:user:garden",
		"predicate": "prefers",
		"object":    "手冲咖啡",
	}

	first := callTool(t, session, "memory_claim", args)
	if first.IsError {
		t.Fatalf("首次写入失败: %s", toolText(first))
	}
	if !strings.Contains(toolText(first), "已记录") {
		t.Fatalf("首次写入应报告新建，实际: %s", toolText(first))
	}

	second := callTool(t, session, "memory_claim", args)
	if !strings.Contains(toolText(second), "已存在") {
		t.Fatalf("重复写入应报告去重，实际: %s", toolText(second))
	}
}

func TestMCPClaimRejectsEmptySubject(t *testing.T) {
	session, _ := newTestSession(t)

	result := callTool(t, session, "memory_claim", map[string]any{
		"subject":   "",
		"predicate": "prefers",
		"object":    "手冲咖啡",
	})

	if !result.IsError {
		t.Fatal("空 subject 应返回错误结果")
	}
}

func TestMCPRecallMarksCurrentValue(t *testing.T) {
	session, mem := newTestSession(t)

	stale, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "drinks",
		Object:     "手冲咖啡",
		ObservedAt: "2026-09-01T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("写入: %v", err)
	}

	if _, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "drinks",
		Object:     "气泡水",
		ObservedAt: "2026-09-14T10:00:00+08:00",
		Supersedes: stale.ID,
	}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	result := callTool(t, session, "memory_recall", map[string]any{"query": "咖啡"})
	if result.IsError {
		t.Fatalf("召回失败: %s", toolText(result))
	}

	text := toolText(result)
	if !strings.Contains(text, "已被后续记录更新") {
		t.Fatalf("召回结果应标注该条已被更新，实际:\n%s", text)
	}
}

func TestMCPTimelineRequiresArgument(t *testing.T) {
	session, _ := newTestSession(t)

	result := callTool(t, session, "memory_timeline", map[string]any{})
	if !result.IsError {
		t.Fatal("既无 subject 也无 predicate 时应返回错误结果")
	}
}

func TestMCPTimelineReportsTruncation(t *testing.T) {
	session, mem := newTestSession(t)
	subject := "urn:alaya:test:trunc"

	for i := 0; i < 5; i++ {
		if _, _, err := mem.Add(models.Event{
			Subject:    subject,
			Predicate:  "p",
			Object:     fmt.Sprintf("v%d", i),
			ObservedAt: fmt.Sprintf("2026-09-0%dT10:00:00+08:00", i+1),
		}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}

	result := callTool(t, session, "memory_timeline", map[string]any{
		"subject": subject,
		"limit":   2,
	})
	if result.IsError {
		t.Fatalf("查询失败: %s", toolText(result))
	}

	text := toolText(result)
	if !strings.Contains(text, "未显示") {
		t.Fatalf("超过条数上限时应提示截断，实际:\n%s", text)
	}
	if !strings.Contains(text, "仅显示最早 2 条") {
		t.Fatalf("应说明显示了几条，实际:\n%s", text)
	}
}

// 畸形时间戳在写入端即被拒绝——读取端的容忍不应成为写入端放行的理由。
func TestMCPRejectsMalformedObservedAt(t *testing.T) {
	session, _ := newTestSession(t)

	result := callTool(t, session, "memory_claim", map[string]any{
		"subject":     "urn:alaya:test:bad-time",
		"predicate":   "p",
		"object":      "v",
		"observed_at": "2026-13-45T99:99:99+08:00",
	})

	if !result.IsError {
		t.Fatalf("畸形时间戳应返回工具错误，实际: %s", toolText(result))
	}
	if text := toolText(result); !strings.Contains(text, "RFC3339") {
		t.Fatalf("错误信息应说明格式要求，实际: %s", text)
	}
}
