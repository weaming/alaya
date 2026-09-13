// Package config 处理运行时的外部配置：数据目录、时区与日志。
package config

import (
	"fmt"
	"io"
	"log"
	"os"
	"time"
)

const (
	defaultTimezone = "Asia/Hong_Kong"
	defaultDataDir  = ".alaya"
)

// ConfigureTimezone 按 TZ 设置本地时区；加载失败时退回固定偏移而非报错退出。
func ConfigureTimezone() {
	name := GetEnv("TZ", defaultTimezone)

	location, err := time.LoadLocation(name)
	if err != nil {
		location = time.FixedZone(defaultTimezone, 8*60*60)
	}

	time.Local = location
}

// ConfigureLogging 把日志定向到 stderr 并统一前缀与时间戳格式。
//
// stdout 留给 MCP 的 JSON-RPC 流，任何写入都会破坏协议。
func ConfigureLogging() {
	log.SetFlags(0)
	log.SetPrefix("")
	log.SetOutput(prefixWriter{output: os.Stderr, prefix: "[alaya] "})
}

type prefixWriter struct {
	output io.Writer
	prefix string
}

func (w prefixWriter) Write(message []byte) (int, error) {
	stamp := time.Now().Format(time.RFC3339)
	line := fmt.Sprintf("%s[%s] %s", w.prefix, stamp, message)

	if _, err := w.output.Write([]byte(line)); err != nil {
		return 0, err
	}

	return len(message), nil
}

// DataDir 返回数据目录：ALAYA_HOME 优先，否则 ~/.alaya。
func DataDir() (string, error) {
	if custom := os.Getenv("ALAYA_HOME"); custom != "" {
		return custom, nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("定位用户主目录: %w", err)
	}

	return home + string(os.PathSeparator) + defaultDataDir, nil
}

// NowISO 返回当前时区的 ISO 8601 时间戳。
func NowISO() string {
	return time.Now().Format(time.RFC3339)
}

// GetEnv 读取环境变量，缺失时返回默认值。
func GetEnv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
