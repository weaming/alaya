package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// HTTP 模式是 stdio 之外的另一条传输路径，且天然引入多客户端并发——
// 而 MCP server 全程持有单个 Memory 实例。这里覆盖它的读写与并发。

// freePort 借内核分配一个空闲端口后立即释放。
// 释放到复用之间存在理论上的竞态窗口，对本地测试足够。
func freePort(t *testing.T) int {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("探测空闲端口: %v", err)
	}
	defer listener.Close()

	return listener.Addr().(*net.TCPAddr).Port
}

// startMCPHTTP 以 HTTP 模式启动 server 并等待其就绪，返回 base URL。
func startMCPHTTP(t *testing.T, home string) string {
	t.Helper()

	addr := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	baseURL := "http://" + addr

	cmd := exec.Command(binaryPath, "mcp", "--http", addr)
	cmd.Env = append(os.Environ(), "ALAYA_HOME="+home)
	cmd.Stderr = io.Discard

	if err := cmd.Start(); err != nil {
		t.Fatalf("启动 HTTP MCP server: %v", err)
	}

	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	waitForHealth(t, baseURL)

	return baseURL
}

// waitForHealth 轮询 /health 直到 server 就绪。
func waitForHealth(t *testing.T, baseURL string) {
	t.Helper()

	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(responseTimeout)

	for time.Now().Before(deadline) {
		response, err := client.Get(baseURL + "/health")
		if err == nil {
			response.Body.Close()

			if response.StatusCode == http.StatusOK {
				return
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatal("等待 HTTP server 就绪超时")
}

// postMCPRaw 发送一次 JSON-RPC 请求并解析 SSE 响应，不依赖 testing.T，
// 以便在并发场景中收集错误。
func postMCPRaw(baseURL string, payload map[string]any) (map[string]any, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("编码请求: %w", err)
	}

	request, err := http.NewRequest(http.MethodPost, baseURL+"/mcp", bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("构造请求: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")

	client := &http.Client{Timeout: responseTimeout}

	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("发送请求: %w", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP 状态码 %d", response.StatusCode)
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应: %w", err)
	}

	data := extractSSEData(string(body))
	if data == "" {
		return nil, fmt.Errorf("响应中没有 data 行: %q", body)
	}

	var parsed map[string]any
	if err := json.Unmarshal([]byte(data), &parsed); err != nil {
		return nil, fmt.Errorf("解析响应 %q: %w", data, err)
	}

	if failure, ok := parsed["error"]; ok {
		return nil, fmt.Errorf("服务端返回错误: %v", failure)
	}

	return parsed, nil
}

func postMCP(t *testing.T, baseURL string, payload map[string]any) map[string]any {
	t.Helper()

	parsed, err := postMCPRaw(baseURL, payload)
	if err != nil {
		t.Fatalf("%v", err)
	}

	return parsed
}

func extractSSEData(body string) string {
	for _, line := range strings.Split(body, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			return data
		}
	}

	return ""
}

// callToolOverHTTP 调用一个工具并返回其文本内容。
func callToolOverHTTP(t *testing.T, baseURL, name string, args map[string]any) string {
	t.Helper()

	response := postMCP(t, baseURL, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
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

	return builder.String()
}

func TestMCPOverHTTP(t *testing.T) {
	home := t.TempDir()
	baseURL := startMCPHTTP(t, home)

	text := callToolOverHTTP(t, baseURL, "memory_claim", map[string]any{
		"subject":   testSubject,
		"predicate": "lives_in",
		"object":    "Hong Kong",
	})
	if !strings.Contains(text, "已记录") {
		t.Fatalf("写入失败: %s", text)
	}

	text = callToolOverHTTP(t, baseURL, "memory_recall", map[string]any{"query": "lives_in"})
	if !strings.Contains(text, "Hong Kong") {
		t.Fatalf("召回应命中，实际: %s", text)
	}
	if !strings.Contains(text, "[当前]") {
		t.Fatalf("应标注为当前值，实际: %s", text)
	}

	// HTTP 与 CLI 必须共享同一份真源
	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("HTTP 写入后应能通过校验，实际: %s", out)
	}
	if out := runCLI(t, home, "stats"); !strings.Contains(out, "事件总数: 1") {
		t.Fatalf("CLI 应看到 HTTP 写入的记录，实际:\n%s", out)
	}
}

// HTTP 天然支持多客户端并发，而 server 持有单个 Memory 实例，
// 写入必须仍然串行化，否则会写出重复序号。
func TestMCPHTTPConcurrentWriters(t *testing.T) {
	const total = 20

	home := t.TempDir()
	baseURL := startMCPHTTP(t, home)

	var (
		wait sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	for i := 0; i < total; i++ {
		wait.Add(1)

		go func(id int) {
			defer wait.Done()

			_, err := postMCPRaw(baseURL, map[string]any{
				"jsonrpc": "2.0",
				"id":      id,
				"method":  "tools/call",
				"params": map[string]any{
					"name": "memory_claim",
					"arguments": map[string]any{
						"subject":   testSubject,
						"predicate": fmt.Sprintf("p%02d", id),
						"object":    fmt.Sprintf("v%d", id),
					},
				},
			})
			if err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		}(i)
	}

	wait.Wait()

	for _, err := range errs {
		t.Errorf("并发写入: %v", err)
	}

	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("并发写入后应仍能通过校验，实际: %s", out)
	}

	want := fmt.Sprintf("事件总数: %d", total)
	if out := runCLI(t, home, "stats"); !strings.Contains(out, want) {
		t.Fatalf("应有 %d 条事件，实际:\n%s", total, out)
	}
}

// claimPayload 构造一次 memory_claim 调用的 JSON-RPC 载荷。
func claimPayload(predicate, object string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name": "memory_claim",
			"arguments": map[string]any{
				"subject":   testSubject,
				"predicate": predicate,
				"object":    object,
			},
		},
	}
}

// `.lock` 是数据目录的常驻文件。删除它（`rm -rf ~/.alaya/*` 这类重置动作都会）后，
// 长驻 server 的锁作用于已被 unlink 的旧 inode，与新进程创建的新 inode 互不相干——
// 双方都以为自己独占，随后各自基于过期的链尾追加，序号必然重复。
//
// 顺序场景下指纹检测能兜住（下次写入前发现文件变了并重载），
// 只有并发才能落进「文件未变」的窗口，因此必须并发复现。
func TestHTTPWritersAfterLockDeletion(t *testing.T) {
	const writers = 10

	home := t.TempDir()
	baseURL := startMCPHTTP(t, home)

	if _, err := postMCPRaw(baseURL, claimPayload("seed", "seed")); err != nil {
		t.Fatalf("初始写入: %v", err)
	}

	if err := os.Remove(filepath.Join(home, ".lock")); err != nil {
		t.Fatalf("删除锁文件: %v", err)
	}

	var (
		wait sync.WaitGroup
		mu   sync.Mutex
		errs []error
	)

	// server 侧并发：它的锁此时作用于已被 unlink 的旧 inode
	for i := 0; i < writers; i++ {
		wait.Add(1)

		go func(id int) {
			defer wait.Done()

			payload := claimPayload(fmt.Sprintf("http%02d", id), fmt.Sprintf("v%d", id))
			if _, err := postMCPRaw(baseURL, payload); err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("HTTP 写入 %d: %w", id, err))
				mu.Unlock()
			}
		}(i)
	}

	// CLI 侧并发：各自新建锁文件，即新 inode
	for i := 0; i < writers; i++ {
		wait.Add(1)

		go func(id int) {
			defer wait.Done()

			_, stderr, err := execCLI(t, home, "claim",
				"--subject", testSubject,
				"--predicate", fmt.Sprintf("cli%02d", id),
				"--object", fmt.Sprintf("v%d", id))
			if err != nil {
				mu.Lock()
				errs = append(errs, fmt.Errorf("CLI 写入 %d: %v (%s)", id, err, stderr))
				mu.Unlock()
			}
		}(i)
	}

	wait.Wait()

	for _, err := range errs {
		t.Errorf("%v", err)
	}

	if out := runCLI(t, home, "verify"); !strings.Contains(out, "校验通过") {
		t.Fatalf("删除锁文件后并发写入应仍合法，实际:\n%s", out)
	}

	// 锁失效还会造成数据丢失：加载失败的写者直接退出，其写入未落盘
	want := fmt.Sprintf("事件总数: %d", writers*2+1)
	if out := runCLI(t, home, "stats"); !strings.Contains(out, want) {
		t.Fatalf("应有 %d 条事件，实际:\n%s", writers*2+1, out)
	}
}
