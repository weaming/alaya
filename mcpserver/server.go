// Package mcpserver 把记忆能力暴露为 MCP 工具。
//
// 工具描述是模型判断"何时该调用"的唯一依据，因此写清楚适用场景与彼此的差别，
// 尤其是 search 与 recall 的分工。
package mcpserver

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weaming/alaya/memory"
)

// server 持有各工具共享的依赖。
type server struct {
	mem *memory.Memory
}

// New 装配 MCP server 与全部记忆工具。
func New(mem *memory.Memory, version string) *mcp.Server {
	instance := &server{mem: mem}

	mcpServer := mcp.NewServer(&mcp.Implementation{
		Name:    "alaya",
		Version: version,
	}, nil)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "memory_claim",
		Description: "记录一条关于用户或世界的断言。" +
			"当用户透露偏好、做出决定、陈述事实，或你希望跨会话记住某件事时调用。" +
			"内容相同的重复写入会自动去重。" +
			"若要更正已有记录，请把旧记录的 id 填入 supersedes 并写入新值，" +
			"而不要另写一条与原值互斥的断言——后者会让系统无法分辨哪个是当前值。",
	}, instance.handleClaim)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "memory_search",
		Description: "按关键词检索已记住的内容，返回条目列表，用于主动查证或深挖细节。" +
			"与 memory_recall 的分工：search 是你按需查询；recall 面向注入，" +
			"返回已按预算排好版、并标注了当前值与历史值的文本块。",
	}, instance.handleSearch)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "memory_recall",
		Description: "按关键词召回相关记忆，返回可直接使用的上下文块。" +
			"在开始一段需要背景的对话或任务前调用，避免让用户重复陈述已说过的事。" +
			"返回内容已标注哪条是当前值、哪条是历史值，请优先采用标注为当前的那条。" +
			"注意匹配的是关键词而非语义：换用同义词可能召回不到，可用 memory_search 先用别名试探。",
	}, instance.handleRecall)

	mcp.AddTool(mcpServer, &mcp.Tool{
		Name: "memory_timeline",
		Description: "追溯某个主体或某个维度的完整演变，用于回答\"我以前怎么看\"这类问题，" +
			"或在发现记忆冲突时查看历史脉络。",
	}, instance.handleTimeline)

	return mcpServer
}
