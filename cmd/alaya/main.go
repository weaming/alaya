// alaya 是记忆底座的命令行入口。
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/weaming/alaya/config"
	"github.com/weaming/alaya/memory"
)

const version = "0.1.0"

func main() {
	config.ConfigureTimezone()
	config.ConfigureLogging()

	if len(os.Args) < 2 {
		printUsage()
		os.Exit(2)
	}

	switch os.Args[1] {
	case "mcp":
		runMCP(os.Args[2:])
	case "claim":
		runClaim(os.Args[2:])
	case "search":
		runSearch(os.Args[2:])
	case "history":
		runHistory(os.Args[2:])
	case "stats":
		runStats()
	case "verify":
		runVerify()
	case "rebuild":
		runRebuild()
	case "healthcheck":
		runHealthCheck()
	case "version", "-v", "--version":
		fmt.Println(version)
	case "help", "-h", "--help":
		printUsage()
	default:
		fmt.Fprintf(os.Stderr, "未知子命令 %q\n\n", os.Args[1])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `alaya %s —— 个人记忆底座

用法:
  alaya mcp [--http :8901]                            启动 MCP server（缺省走 stdio）
  alaya claim --subject S --predicate P --object O    写入一条断言
  alaya search --query Q [--limit N]                  检索记忆
  alaya history --subject S [--predicate P]           查看某主体的记忆演变
  alaya stats                                         统计数据概况
  alaya verify                                        校验事件日志的完整性
  alaya rebuild                                       从事件日志重建 INDEX.md

标志须置于位置参数之前，故此处一律使用具名标志。
数据目录: $ALAYA_HOME，缺省 ~/.alaya
`, version)
}

// openMemory 打开数据目录并返回记忆实例。
func openMemory() *memory.Memory {
	dir, err := config.DataDir()
	if err != nil {
		log.Fatalf("定位数据目录: %v", err)
	}

	mem, err := memory.Open(dir)
	if err != nil {
		log.Fatalf("打开记忆: %v", err)
	}

	return mem
}
