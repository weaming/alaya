package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const (
	protocolVersion = "2025-06-18"
	responseTimeout = 5 * time.Second

	testSubject = "urn:alaya:person:user:garden"
)

// binaryPath 是 TestMain 编译出的被测二进制。
var binaryPath string

// TestMain 编译一次二进制，供全部集成测试从外部视角驱动。
func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	dir, err := os.MkdirTemp("", "alaya-integration")
	if err != nil {
		fmt.Fprintf(os.Stderr, "创建临时目录: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	binaryPath = filepath.Join(dir, "alaya")

	build := exec.Command("go", "build", "-o", binaryPath, ".")
	build.Stderr = os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "编译 alaya: %v\n", err)
		return 1
	}

	return m.Run()
}

// runCLI 执行一条 CLI 命令，返回 stdout；命令失败则终止测试。
func runCLI(t *testing.T, home string, args ...string) string {
	t.Helper()

	stdout, stderr, err := execCLI(t, home, args...)
	if err != nil {
		t.Fatalf("执行 alaya %v 失败: %v\nstderr: %s", args, err, stderr)
	}

	return stdout
}

// runCLIExpectingFailure 执行一条预期失败的命令，返回 stderr。
func runCLIExpectingFailure(t *testing.T, home string, args ...string) string {
	t.Helper()

	_, stderr, err := execCLI(t, home, args...)
	if err == nil {
		t.Fatalf("alaya %v 本应失败，却成功了", args)
	}

	return stderr
}

func execCLI(t *testing.T, home string, args ...string) (string, string, error) {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	cmd.Env = append(os.Environ(), "ALAYA_HOME="+home)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	return stdout.String(), stderr.String(), err
}

// mcpSession 是一个通过 stdio 驱动的 MCP server 子进程。
type mcpSession struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	reader *bufio.Reader
	nextID int
}

func startMCP(t *testing.T, home string) *mcpSession {
	t.Helper()

	cmd := exec.Command(binaryPath, "mcp")
	cmd.Env = append(os.Environ(), "ALAYA_HOME="+home)
	cmd.Stderr = io.Discard

	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatalf("获取 stdin: %v", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("获取 stdout: %v", err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 MCP server: %v", err)
	}

	session := &mcpSession{
		cmd:    cmd,
		stdin:  stdin,
		reader: bufio.NewReader(stdout),
	}

	t.Cleanup(func() {
		_ = stdin.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	session.initialize(t)

	return session
}

func (s *mcpSession) initialize(t *testing.T) {
	t.Helper()

	s.request(t, "initialize", map[string]any{
		"protocolVersion": protocolVersion,
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "integration-test", "version": "0"},
	})

	s.notify(t, "notifications/initialized")
}

func (s *mcpSession) request(t *testing.T, method string, params any) map[string]any {
	t.Helper()

	s.nextID++

	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      s.nextID,
		"method":  method,
	}
	if params != nil {
		payload["params"] = params
	}

	s.write(t, payload)

	var response map[string]any
	if err := json.Unmarshal(s.readLine(t), &response); err != nil {
		t.Fatalf("解析响应: %v", err)
	}

	if failure, ok := response["error"]; ok {
		t.Fatalf("服务端返回错误: %v", failure)
	}

	return response
}

func (s *mcpSession) notify(t *testing.T, method string) {
	t.Helper()

	s.write(t, map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	})
}

func (s *mcpSession) write(t *testing.T, payload map[string]any) {
	t.Helper()

	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("编码请求: %v", err)
	}

	if _, err := s.stdin.Write(append(raw, '\n')); err != nil {
		t.Fatalf("写入请求: %v", err)
	}
}

func (s *mcpSession) readLine(t *testing.T) []byte {
	t.Helper()

	type result struct {
		line []byte
		err  error
	}

	ready := make(chan result, 1)
	go func() {
		line, err := s.reader.ReadBytes('\n')
		ready <- result{line, err}
	}()

	select {
	case got := <-ready:
		if got.err != nil {
			t.Fatalf("读取响应: %v", got.err)
		}
		return got.line

	case <-time.After(responseTimeout):
		t.Fatal("等待 MCP 响应超时")
		return nil
	}
}

// callTool 调用一个工具并返回其文本内容。
func (s *mcpSession) callTool(t *testing.T, name string, args map[string]any) (string, bool) {
	t.Helper()

	response := s.request(t, "tools/call", map[string]any{
		"name":      name,
		"arguments": args,
	})

	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("响应缺少 result: %v", response)
	}

	var builder strings.Builder

	if content, ok := result["content"].([]any); ok {
		for _, item := range content {
			block, ok := item.(map[string]any)
			if !ok {
				continue
			}
			if text, ok := block["text"].(string); ok {
				builder.WriteString(text)
			}
		}
	}

	isError, _ := result["isError"].(bool)

	return builder.String(), isError
}

// --- CLI 集成 ---

func TestCLIEndToEnd(t *testing.T) {
	home := t.TempDir()

	claimArgs := []string{
		"claim",
		"--subject", testSubject,
		"--predicate", "prefers",
		"--object", "手冲咖啡",
		"--observed-at", "2026-09-01T10:00:00+08:00",
	}

	t.Run("写入", func(t *testing.T) {
		if out := runCLI(t, home, claimArgs...); !strings.Contains(out, "已记录") {
			t.Fatalf("应报告写入成功，实际: %s", out)
		}
	})

	t.Run("跨进程幂等", func(t *testing.T) {
		out := runCLI(t, home, claimArgs...)
		if !strings.Contains(out, "已存在") {
			t.Fatalf("重复写入应被去重，实际: %s", out)
		}

		// 日志中应只有一行
		raw, err := os.ReadFile(filepath.Join(home, "events.jsonl"))
		if err != nil {
			t.Fatalf("读取日志: %v", err)
		}
		if lines := strings.Count(strings.TrimSpace(string(raw)), "\n"); lines != 0 {
			t.Fatalf("重复写入产生了额外记录，日志有 %d 行", lines+1)
		}
	})

	t.Run("检索", func(t *testing.T) {
		if out := runCLI(t, home, "search", "--query", "咖啡"); !strings.Contains(out, "手冲咖啡") {
			t.Fatalf("检索应命中已写入的记忆，实际: %s", out)
		}
	})

	t.Run("演变", func(t *testing.T) {
		if out := runCLI(t, home, "history", "--subject", testSubject); !strings.Contains(out, "prefers") {
			t.Fatalf("应返回该主体的演变，实际: %s", out)
		}
	})

	t.Run("统计", func(t *testing.T) {
		out := runCLI(t, home, "stats")
		for _, want := range []string{"事件总数: 1", "当前值: 1", "主体数: 1"} {
			if !strings.Contains(out, want) {
				t.Fatalf("统计应包含 %q，实际:\n%s", want, out)
			}
		}
	})

	t.Run("索引重建", func(t *testing.T) {
		runCLI(t, home, "rebuild")

		index, err := os.ReadFile(filepath.Join(home, "INDEX.md"))
		if err != nil {
			t.Fatalf("读取索引: %v", err)
		}

		for _, want := range []string{"# Alaya 记忆索引", "## 当前值", "手冲咖啡"} {
			if !strings.Contains(string(index), want) {
				t.Fatalf("索引应包含 %q，实际:\n%s", want, index)
			}
		}
	})
}

func TestCLISupersedeFlow(t *testing.T) {
	home := t.TempDir()

	if out := runCLI(t, home, "claim",
		"--subject", testSubject, "--predicate", "prefers", "--object", "espresso",
		"--observed-at", "2026-09-01T10:00:00+08:00"); !strings.Contains(out, "已记录") {
		t.Fatalf("写入旧值失败: %s", out)
	}

	oldID := extractID(t, runCLI(t, home, "search", "--query", "espresso"))

	runCLI(t, home, "claim",
		"--subject", testSubject, "--predicate", "prefers", "--object", "hand_drip",
		"--observed-at", "2026-09-14T10:00:00+08:00", "--supersedes", oldID)

	out := runCLI(t, home, "search", "--query", "prefers")

	if !strings.Contains(out, "[当前]") {
		t.Fatalf("应标注出当前值，实际:\n%s", out)
	}
	if !strings.Contains(out, "[历史]") {
		t.Fatalf("应标注出历史值，实际:\n%s", out)
	}
	if !strings.Contains(out, "已被后续记录更新") {
		t.Fatalf("被取代的条目应提示已更新，实际:\n%s", out)
	}

	// 取代关系必须真的落到基岩里，而不只是显示层的推断
	raw, err := os.ReadFile(filepath.Join(home, "events.jsonl"))
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}
	if !strings.Contains(string(raw), oldID) {
		t.Fatalf("日志中应记录 supersedes 指向的 %s", oldID)
	}

	if out := runCLI(t, home, "stats"); !strings.Contains(out, "当前值: 1") || !strings.Contains(out, "历史值: 1") {
		t.Fatalf("统计应区分当前值与历史值，实际:\n%s", out)
	}
}

// 打错 supersedes 的 id 会让更正在无声中失效——用户以为旧值已更正，实际没有。
// 这必须报错，而不是安静地写入一条无效的取代关系。
func TestCLIRejectsUnknownSupersedes(t *testing.T) {
	home := t.TempDir()

	stderr := runCLIExpectingFailure(t, home, "claim",
		"--subject", testSubject, "--predicate", "prefers", "--object", "x",
		"--supersedes", "sha256:0000000000000000000000000000000000000000000000000000000000000000")

	if !strings.Contains(stderr, "不存在") {
		t.Fatalf("应报告取代目标不存在，实际 stderr: %s", stderr)
	}

	// 失败的写入不得留下任何记录
	if _, err := os.Stat(filepath.Join(home, "events.jsonl")); err == nil {
		raw, _ := os.ReadFile(filepath.Join(home, "events.jsonl"))
		if len(strings.TrimSpace(string(raw))) > 0 {
			t.Fatalf("被拒绝的写入不应落盘，实际日志:\n%s", raw)
		}
	}
}

func TestCLIDetectsTampering(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "prefers", "--object", "hand_drip")
	runCLI(t, home, "verify")

	path := filepath.Join(home, "events.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}

	tampered := strings.Replace(string(raw), "hand_drip", "espresso_", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("写入篡改内容: %v", err)
	}

	stderr := runCLIExpectingFailure(t, home, "verify")
	if !strings.Contains(stderr, "校验失败") {
		t.Fatalf("篡改后 verify 应失败，实际 stderr: %s", stderr)
	}
}

func extractID(t *testing.T, output string) string {
	t.Helper()

	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) >= 2 && fields[0] == "id:" {
			return fields[1]
		}
	}

	t.Fatalf("输出中未找到 id:\n%s", output)
	return ""
}

// --- MCP 集成 ---

func TestMCPOverStdio(t *testing.T) {
	home := t.TempDir()
	session := startMCP(t, home)

	response := session.request(t, "tools/list", nil)

	result, ok := response["result"].(map[string]any)
	if !ok {
		t.Fatalf("tools/list 响应缺少 result: %v", response)
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("tools/list 响应缺少 tools: %v", result)
	}
	if len(tools) != 4 {
		t.Fatalf("应注册 4 个工具，实际 %d 个", len(tools))
	}

	text, isError := session.callTool(t, "memory_claim", map[string]any{
		"subject":   testSubject,
		"predicate": "prefers",
		"object":    "手冲咖啡",
	})
	if isError {
		t.Fatalf("写入失败: %s", text)
	}
	if !strings.Contains(text, "已记录") {
		t.Fatalf("应报告写入成功，实际: %s", text)
	}

	text, isError = session.callTool(t, "memory_recall", map[string]any{"query": "咖啡"})
	if isError {
		t.Fatalf("召回失败: %s", text)
	}
	if !strings.Contains(text, "手冲咖啡") {
		t.Fatalf("召回应包含已写入的记忆，实际: %s", text)
	}
}

// 这是 "马上投入使用" 的核心承诺：写入后进程退出，新进程仍能召回。
func TestMCPRemembersAcrossRestart(t *testing.T) {
	home := t.TempDir()

	first := startMCP(t, home)
	if text, isError := first.callTool(t, "memory_claim", map[string]any{
		"subject":     testSubject,
		"predicate":   "lives_in",
		"object":      "Hong Kong",
		"observed_at": "2026-09-01T10:00:00+08:00",
	}); isError {
		t.Fatalf("写入失败: %s", text)
	}

	// 关闭第一个 server，模拟会话结束
	_ = first.stdin.Close()
	_ = first.cmd.Wait()

	second := startMCP(t, home)

	text, isError := second.callTool(t, "memory_recall", map[string]any{"query": "lives_in"})
	if isError {
		t.Fatalf("重启后召回失败: %s", text)
	}
	if !strings.Contains(text, "Hong Kong") {
		t.Fatalf("重启后应仍能召回，实际: %s", text)
	}
	if !strings.Contains(text, "[当前]") {
		t.Fatalf("应标注为当前值，实际: %s", text)
	}
}

func TestMCPReportsToolErrors(t *testing.T) {
	session := startMCP(t, t.TempDir())

	text, isError := session.callTool(t, "memory_claim", map[string]any{
		"subject":   "",
		"predicate": "prefers",
		"object":    "x",
	})

	if !isError {
		t.Fatalf("空 subject 应返回错误结果，实际: %s", text)
	}
	if !strings.Contains(text, "subject") {
		t.Fatalf("错误信息应指明问题所在，实际: %s", text)
	}
}

func TestMCPRejectsUnknownSupersedes(t *testing.T) {
	session := startMCP(t, t.TempDir())

	text, isError := session.callTool(t, "memory_claim", map[string]any{
		"subject":    testSubject,
		"predicate":  "prefers",
		"object":     "x",
		"supersedes": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
	})

	if !isError {
		t.Fatalf("取代目标不存在时应返回工具错误，实际: %s", text)
	}
	if !strings.Contains(text, "不存在") {
		t.Fatalf("错误信息应指明原因，实际: %s", text)
	}
}

func lineContaining(t *testing.T, text, needle string) string {
	t.Helper()

	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}

	t.Fatalf("输出中未找到包含 %q 的行:\n%s", needle, text)
	return ""
}

// 省略 observed_at 是模型的默认调用方式，此时后写入的值必须成为当前值。
// 判定若退化为"先写入者胜出"，模型按工具描述执行 supersedes 更正的承诺即告失效。
func TestMCPWithoutObservedAtKeepsWriteOrder(t *testing.T) {
	home := t.TempDir()
	session := startMCP(t, home)

	for _, object := range []string{"深圳", "香港"} {
		if text, isError := session.callTool(t, "memory_claim", map[string]any{
			"subject":   testSubject,
			"predicate": "lives_in",
			"object":    object,
		}); isError {
			t.Fatalf("写入 %s 失败: %s", object, text)
		}
	}

	text, isError := session.callTool(t, "memory_timeline", map[string]any{"subject": testSubject})
	if isError {
		t.Fatalf("查询演变失败: %s", text)
	}

	if line := lineContaining(t, text, "香港"); !strings.Contains(line, "[当前]") {
		t.Fatalf("后写入的香港应为当前值，实际该行: %s", line)
	}
	if line := lineContaining(t, text, "深圳"); !strings.Contains(line, "[历史]") {
		t.Fatalf("先写入的深圳应为历史值，实际该行: %s", line)
	}
}

// README 同时提供 CLI 与长驻 MCP server 两条写路径，二者共用同一数据目录。
// 混写若不串行化，长驻进程会基于过期的链尾追加，写出重复序号——
// 而链校验失败会让整份日志拒绝加载，等于整个记忆库报废。
func TestMixedWritersDoNotBreakChain(t *testing.T) {
	home := t.TempDir()
	session := startMCP(t, home)

	claim := func(object string) {
		t.Helper()

		if text, isError := session.callTool(t, "memory_claim", map[string]any{
			"subject":   testSubject,
			"predicate": "lives_in",
			"object":    object,
		}); isError {
			t.Fatalf("MCP 写入 %s 失败: %s", object, text)
		}
	}

	claim("深圳")

	// CLI 是独立进程，会看到 server 写入的那条
	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "lives_in", "--object", "香港")

	// 长驻 server 的内存链尾此时已过期，续写必须仍然成立
	claim("上海")

	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("混写后应仍能通过校验，实际: %s", out)
	}

	if out := runCLI(t, home, "stats"); !strings.Contains(out, "事件总数: 3") {
		t.Fatalf("应有 3 条事件，实际:\n%s", out)
	}

	// 三条都必须能被正常读取，且顺序正确
	text, isError := session.callTool(t, "memory_timeline", map[string]any{"subject": testSubject})
	if isError {
		t.Fatalf("混写后查询失败: %s", text)
	}
	if line := lineContaining(t, text, "上海"); !strings.Contains(line, "[当前]") {
		t.Fatalf("最后写入的上海应为当前值，实际该行: %s", line)
	}
}

// 数据目录在 server 存活期间被外部改动（删库重来、从备份恢复）后，
// 该 server 的后续写入必须仍然写出合法链——这是常规运维动作，不该要求先停 server。
func TestMCPWritesAfterExternalTruncation(t *testing.T) {
	home := t.TempDir()
	session := startMCP(t, home)

	if text, isError := session.callTool(t, "memory_claim", map[string]any{
		"subject": testSubject, "predicate": "lives_in", "object": "深圳",
	}); isError {
		t.Fatalf("写入失败: %s", text)
	}

	// 外部清空日志
	if err := os.WriteFile(filepath.Join(home, "events.jsonl"), nil, 0o644); err != nil {
		t.Fatalf("清空日志: %v", err)
	}

	if text, isError := session.callTool(t, "memory_claim", map[string]any{
		"subject": testSubject, "predicate": "lives_in", "object": "香港",
	}); isError {
		t.Fatalf("清空后写入失败: %s", text)
	}

	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("清空后写入应仍合法，实际: %s", out)
	}

	if out := runCLI(t, home, "stats"); !strings.Contains(out, "事件总数: 1") {
		t.Fatalf("清空后应只有 1 条事件，实际:\n%s", out)
	}
}

// observed_at 接受 RFC3339，调用方可传任意偏移；
// 字符串只在偏移相同时保持时间序，跨偏移必须按时刻比较。
func TestCLICurrentValueAcrossTimezones(t *testing.T) {
	home := t.TempDir()
	subject := "urn:alaya:test:tz"

	// 实际更晚的一条：02:00Z
	runCLI(t, home, "claim", "--subject", subject, "--predicate", "departs_at",
		"--object", "flight-A(02:00Z)", "--observed-at", "2026-09-14T02:00:00+00:00")

	// 实际更早的一条：09:00+08:00 即 01:00Z，字典序却更大
	runCLI(t, home, "claim", "--subject", subject, "--predicate", "departs_at",
		"--object", "flight-B(01:00Z)", "--observed-at", "2026-09-14T09:00:00+08:00")

	out := runCLI(t, home, "history", "--subject", subject)

	if line := lineContaining(t, out, "flight-A"); !strings.Contains(line, "[当前]") {
		t.Fatalf("02:00Z 应为当前值，实际该行: %s", line)
	}
	if line := lineContaining(t, out, "flight-B"); !strings.Contains(line, "[历史]") {
		t.Fatalf("09:00+08:00 实际更早，应为历史值，实际该行: %s", line)
	}
}

// 只读挂载、备份副本、容器只读卷下，查询与校验必须可用——
// 它们不需要写权限，不应因无法创建锁文件而失效。
func TestCLIReadOnlyDataDirectory(t *testing.T) {
	home := t.TempDir()

	runCLI(t, home, "claim", "--subject", testSubject, "--predicate", "p", "--object", "v")

	if err := os.Remove(filepath.Join(home, ".lock")); err != nil {
		t.Fatalf("删除锁文件: %v", err)
	}
	if err := os.Chmod(home, 0o555); err != nil {
		t.Fatalf("设为只读: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(home, 0o755) })

	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("只读目录下 verify 应可用，实际: %s", out)
	}
	if out := runCLI(t, home, "search", "--query", "v"); !strings.Contains(out, "v") {
		t.Fatalf("只读目录下 search 应可用，实际: %s", out)
	}
	if out := runCLI(t, home, "stats"); !strings.Contains(out, "事件总数: 1") {
		t.Fatalf("只读目录下 stats 应可用，实际:\n%s", out)
	}

	stderr := runCLIExpectingFailure(t, home, "claim",
		"--subject", testSubject, "--predicate", "p2", "--object", "v2")
	if !strings.Contains(stderr, "写锁") {
		t.Fatalf("只读目录下写入应报告写锁不可用，实际: %s", stderr)
	}
}
