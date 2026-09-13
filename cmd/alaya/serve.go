package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/weaming/alaya/config"
	"github.com/weaming/alaya/mcpserver"
)

const healthTimeout = 5 * time.Second

func runMCP(args []string) {
	flags := flag.NewFlagSet("mcp", flag.ExitOnError)
	httpAddr := flags.String("http", "", "以 Streamable HTTP 方式监听该地址（如 :8901）；缺省走 stdio")
	flags.Parse(args)

	mem := openMemory()
	defer mem.Close()

	server := mcpserver.New(mem, version)

	if *httpAddr == "" {
		log.Printf("以 stdio 启动 MCP server，数据目录 %s", mem.Dir())
		if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
			log.Fatalf("MCP server 退出: %v", err)
		}
		return
	}

	serveHTTP(server, *httpAddr, mem.Dir())
}

// serveHTTP 以 Streamable HTTP 暴露 MCP，并附带 /health 供容器探测。
func serveHTTP(server *mcp.Server, addr, dir string) {
	handler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return server
	}, &mcp.StreamableHTTPOptions{Stateless: true})

	mux := http.NewServeMux()
	mux.Handle("/mcp", handler)
	mux.HandleFunc("/health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]string{"status": "ok"})
	})

	httpServer := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	log.Printf("以 HTTP 启动 MCP server，监听 %s/mcp，数据目录 %s", addr, dir)

	if err := httpServer.ListenAndServe(); err != nil {
		log.Fatalf("HTTP server 退出: %v", err)
	}
}

// runHealthCheck 供容器健康检查调用，探测自身的 /health。
func runHealthCheck() {
	addr := config.GetEnv("ALAYA_HTTP", "127.0.0.1:8901")

	client := &http.Client{Timeout: healthTimeout}
	response, err := client.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		log.Fatalf("健康检查失败: %v", err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		log.Fatalf("健康检查返回状态码 %d", response.StatusCode)
	}

	log.Printf("健康检查通过")
}
