package memory

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"
)

// maxDisplayRunes 限制单条记忆在列表与注入块里的显示长度。
//
// 文档原文可达上千字，整段铺开会把检索结果冲垮；截断后仍保留足够的
// 辨识度，需要全文时可按 id 追溯或直接读源文件。
const maxDisplayRunes = 120

// Format 渲染单条记忆，标注它是当前值还是历史值。
func (e Entry) Format() string {
	marker := "[历史]"
	if e.IsCurrent {
		marker = "[当前]"
	}

	line := fmt.Sprintf("- %s %s %s %s（记于 %s）",
		marker, e.Event.Subject, e.Event.Predicate,
		abbreviate(e.Event.Object), ShortTime(e.Event.EffectiveObservedAt()))

	if e.SupersededBy != "" {
		line += "（已被后续记录更新）"
	}

	return line
}

// abbreviate 把多行文本压成单行并截断，供列表与注入使用。
func abbreviate(text string) string {
	flat := strings.Join(strings.Fields(text), " ")

	runes := []rune(flat)
	if len(runes) <= maxDisplayRunes {
		return flat
	}

	return string(runes[:maxDisplayRunes]) + "…"
}

// Render 生成可直接拼入 prompt 的文本。
func (r RecallResult) Render() string {
	if len(r.Entries) == 0 {
		return ""
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "## 记忆（%d 条）\n\n", len(r.Entries))

	for _, entry := range r.Entries {
		builder.WriteString(entry.Format())
		builder.WriteByte('\n')
	}

	if r.Truncated > 0 {
		fmt.Fprintf(&builder, "\n（预算 %d token 内装入 %d 条，另有 %d 条更相关的记忆未装入）\n",
			r.Budget, len(r.Entries), r.Truncated)
	}

	return builder.String()
}

// ShortTime 把 RFC3339 时间戳截为日期部分。
//
// 先折算到本地时区再截断，使不同偏移的条目在并列时日期可比——
// 否则同一时刻的 "02:00+00:00" 与 "10:00+08:00" 会显示成相邻两天。
func ShortTime(stamp string) string {
	parsed, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		if len(stamp) >= 10 {
			return stamp[:10]
		}

		return stamp
	}

	return parsed.In(time.Local).Format("2006-01-02")
}

// estimateTokens 保守估计 token 数：以字符数作上界，宁可少装也不超预算。
func estimateTokens(text string) int {
	return utf8.RuneCountInString(text)
}
