package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/weaming/alaya/memory"
)

type claimArgs struct {
	Subject    string `json:"subject" jsonschema:"断言的主体，用稳定 ID（如 urn:alaya:person:user:garden）或可辨识的名称"`
	Predicate  string `json:"predicate" jsonschema:"受控谓词，描述主体与客体之间的关系（如 prefers、decided、lives_in）"`
	Object     string `json:"object" jsonschema:"断言的值或客体"`
	World      string `json:"world,omitempty" jsonschema:"真值所在世界，缺省 world:real"`
	Scope      string `json:"scope,omitempty" jsonschema:"声称者所在的 scope，缺省 user:garden"`
	ObservedAt string `json:"observed_at,omitempty" jsonschema:"该断言在现实中成立的时间（RFC3339），缺省为当前时间"`
	Supersedes string `json:"supersedes,omitempty" jsonschema:"被本条更新取代的旧记录 id；用于更正而非新增"`
	Note       string `json:"note,omitempty" jsonschema:"补充说明或原文出处"`
}

// handleClaim 写入一条声称。内容相同的重复写入会被幂等去重。
func (s *server) handleClaim(_ context.Context, _ *mcp.CallToolRequest, args claimArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Subject) == "" {
		return toolError("subject 不能为空"), nil, nil
	}
	if strings.TrimSpace(args.Predicate) == "" {
		return toolError("predicate 不能为空"), nil, nil
	}

	stored, isNew, err := s.mem.Add(newEvent(args))
	if err != nil {
		if errors.Is(err, memory.ErrInvalidArgument) {
			return toolError(err.Error()), nil, nil
		}

		return nil, nil, fmt.Errorf("写入记忆: %w", err)
	}

	if !isNew {
		return textResult(fmt.Sprintf("该断言已存在，未重复写入。\nid: %s", stored.ID)), nil, nil
	}

	return textResult(fmt.Sprintf("已记录。\nid: %s\n%s %s %s（记于 %s）",
		stored.ID, stored.Subject, stored.Predicate, stored.Object, memory.ShortTime(stored.ObservedAt))), nil, nil
}

type searchArgs struct {
	Query string `json:"query" jsonschema:"检索词，支持中英文"`
	Limit int    `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 10"`
	World string `json:"world,omitempty" jsonschema:"仅返回该世界内的记忆"`
	Scope string `json:"scope,omitempty" jsonschema:"仅返回该 scope 内的记忆"`
}

// handleSearch 按需检索，不受注入预算约束，但受条数上限约束。
func (s *server) handleSearch(_ context.Context, _ *mcp.CallToolRequest, args searchArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Query) == "" {
		return toolError("query 不能为空"), nil, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	entries := s.mem.SearchIn(args.Query, limit, args.World, args.Scope)
	if len(entries) == 0 {
		return textResult("没有找到匹配的记忆。"), nil, nil
	}

	var builder strings.Builder
	for _, entry := range entries {
		fmt.Fprintf(&builder, "%s\n  id: %s  相关度: %.3f\n", entry.Format(), entry.Event.ID, entry.Score)
	}

	return textResult(builder.String()), nil, nil
}

type recallArgs struct {
	Query        string `json:"query" jsonschema:"当前对话或任务的主题，用于召回相关记忆"`
	BudgetTokens int    `json:"budget_tokens,omitempty" jsonschema:"注入预算上限（token），缺省 800"`
}

// handleRecall 装配预算受限的上下文，返回可直接拼入 prompt 的文本。
func (s *server) handleRecall(_ context.Context, _ *mcp.CallToolRequest, args recallArgs) (*mcp.CallToolResult, any, error) {
	result := s.mem.Recall(args.Query, args.BudgetTokens)
	if len(result.Entries) == 0 {
		return textResult("没有与当前主题相关的记忆。"), nil, nil
	}

	return textResult(result.Render()), nil, nil
}

type timelineArgs struct {
	Subject   string `json:"subject,omitempty" jsonschema:"要追溯的主体；与 predicate 至少给出一个"`
	Predicate string `json:"predicate,omitempty" jsonschema:"限定谓词，用于只看某个维度的演变"`
	Limit     int    `json:"limit,omitempty" jsonschema:"返回条数上限，缺省 50"`
	World     string `json:"world,omitempty" jsonschema:"仅追溯该世界内的演变"`
	Scope     string `json:"scope,omitempty" jsonschema:"仅追溯该 scope 内的演变"`
}

// handleTimeline 返回某主体（或某谓词维度）的完整演变，含与当前值的关系。
func (s *server) handleTimeline(_ context.Context, _ *mcp.CallToolRequest, args timelineArgs) (*mcp.CallToolResult, any, error) {
	if strings.TrimSpace(args.Subject) == "" && strings.TrimSpace(args.Predicate) == "" {
		return toolError("subject 与 predicate 至少给出一个"), nil, nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 50
	}

	entries := s.mem.TimelineIn(args.Subject, args.Predicate, args.World, args.Scope)
	if len(entries) == 0 {
		return textResult("没有找到该主体的记忆。"), nil, nil
	}

	title := args.Subject
	if title == "" {
		title = args.Predicate
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "## %s 的记忆演变（%d 条）\n\n", title, len(entries))

	for i, entry := range entries {
		if i >= limit {
			fmt.Fprintf(&builder, "\n（仅显示最早 %d 条，另有 %d 条未显示）\n", limit, len(entries)-limit)
			break
		}

		fmt.Fprintf(&builder, "%s\n  id: %s\n", entry.Format(), entry.Event.ID)
	}

	return textResult(builder.String()), nil, nil
}

// textResult 返回纯文本工具结果。
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// toolError 返回标记为错误的工具结果；错误信息对调用方可见，便于模型自行纠正。
func toolError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: message}},
		IsError: true,
	}
}
