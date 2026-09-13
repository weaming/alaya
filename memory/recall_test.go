package memory

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/weaming/alaya/models"
)

func newTestMemory(t *testing.T) *Memory {
	t.Helper()

	mem, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("创建记忆: %v", err)
	}
	t.Cleanup(func() {
		if err := mem.Close(); err != nil {
			t.Errorf("关闭记忆: %v", err)
		}
	})

	return mem
}

// 显式 supersedes 应让旧值让位给新值。
func TestRecallMarksSupersededValueAsHistorical(t *testing.T) {
	mem := newTestMemory(t)

	old, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "prefers",
		Object:     "espresso",
		ObservedAt: "2026-09-01T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("写入旧值: %v", err)
	}

	current, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "prefers",
		Object:     "hand_drip",
		ObservedAt: "2026-09-14T10:00:00+08:00",
		Supersedes: old.ID,
	})
	if err != nil {
		t.Fatalf("写入新值: %v", err)
	}

	result := mem.Recall("prefers", 800)
	if len(result.Entries) != 2 {
		t.Fatalf("应召回 2 条，实际 %d 条", len(result.Entries))
	}

	byID := make(map[string]Entry, len(result.Entries))
	for _, entry := range result.Entries {
		byID[entry.Event.ID] = entry
	}

	if !byID[current.ID].IsCurrent {
		t.Fatal("新值应被标记为当前值")
	}
	if byID[old.ID].IsCurrent {
		t.Fatal("被取代的旧值不应标记为当前值")
	}
	if byID[old.ID].SupersededBy != current.ID {
		t.Fatalf("旧值应指向取代者，实际 %q", byID[old.ID].SupersededBy)
	}
}

// 未显式 supersede 时，后陈述的值应成为当前值。
func TestRecallUsesLatestObservedValue(t *testing.T) {
	mem := newTestMemory(t)

	earlier, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "lives_in",
		Object:     "Shenzhen",
		ObservedAt: "2026-01-01T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("写入: %v", err)
	}

	later, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "lives_in",
		Object:     "Hong Kong",
		ObservedAt: "2026-09-01T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("写入: %v", err)
	}

	result := mem.Recall("lives_in", 800)

	for _, entry := range result.Entries {
		switch entry.Event.ID {
		case later.ID:
			if !entry.IsCurrent {
				t.Fatal("较晚陈述的值应为当前值")
			}
		case earlier.ID:
			if entry.IsCurrent {
				t.Fatal("较早的值不应为当前值")
			}
		}
	}
}

// 当前值判定必须基于全量事件：即便当前值没有命中查询，
// 命中的历史值也必须被正确标注为"已被更新"——否则模型会把旧值当现值用。
func TestRecallResolvesCurrentFromAllEventsNotJustHits(t *testing.T) {
	mem := newTestMemory(t)

	stale, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "drinks",
		Object:     "咖啡",
		ObservedAt: "2026-01-01T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("写入: %v", err)
	}

	// 当前值刻意不含查询词，因此不会被检索命中
	if _, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "drinks",
		Object:     "气泡水",
		ObservedAt: "2026-09-01T10:00:00+08:00",
		Supersedes: stale.ID,
	}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	result := mem.Recall("咖啡", 800)
	if len(result.Entries) == 0 {
		t.Fatal("应至少召回那条历史值")
	}

	entry := result.Entries[0]
	if entry.Event.ID != stale.ID {
		t.Fatalf("命中的应是历史值，实际 %s", entry.Event.Object)
	}
	if entry.IsCurrent {
		t.Fatal("该条已被取代，不应标记为当前值")
	}
	if entry.SupersededBy == "" {
		t.Fatal("应标注它已被后续记录取代")
	}

	if !strings.Contains(result.Render(), "已被后续记录更新") {
		t.Fatal("渲染结果应提示该条已被更新")
	}
}

func TestRecallRespectsBudget(t *testing.T) {
	mem := newTestMemory(t)

	// 每条用不同的谓词，避免同一断言的条数上限先于预算生效
	for i := 0; i < 30; i++ {
		if _, _, err := mem.Add(models.Event{
			Subject:    "urn:alaya:person:user:garden",
			Predicate:  fmt.Sprintf("note_%02d", i),
			Object:     strings.Repeat("很长的记忆内容", 8),
			ObservedAt: "2026-09-01T10:00:00+08:00",
		}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}

	result := mem.Recall("记忆", 200)

	if result.Spent > result.Budget {
		t.Fatalf("装填 %d token 超过预算 %d", result.Spent, result.Budget)
	}
	if result.Truncated == 0 {
		t.Fatal("预算不足时应报告截断条数，而不是静默丢弃")
	}
	if !strings.Contains(result.Render(), "未装入") {
		t.Fatal("渲染结果应说明有多少条因预算未装入")
	}
}

func TestRecallLimitsHistoryPerClaim(t *testing.T) {
	mem := newTestMemory(t)

	// 同一断言的多个历史值不应淹没预算
	for i := 0; i < 10; i++ {
		if _, _, err := mem.Add(models.Event{
			Subject:    "urn:alaya:person:user:garden",
			Predicate:  "prefers",
			Object:     fmt.Sprintf("咖啡%d", i),
			ObservedAt: "2026-0" + string(rune('1'+i%9)) + "-01T10:00:00+08:00",
		}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}

	result := mem.Recall("咖啡", 8000)

	counts := make(map[models.Triple]int)
	for _, entry := range result.Entries {
		counts[entry.Event.Triple()]++
	}

	for key, count := range counts {
		if count > maxPerTriple {
			t.Fatalf("断言 %+v 装入了 %d 条，超过上限 %d", key, count, maxPerTriple)
		}
	}
}

func TestTimelineOrdersByObservedTime(t *testing.T) {
	mem := newTestMemory(t)

	for _, stamp := range []string{"2026-09-14T10:00:00+08:00", "2026-01-01T10:00:00+08:00"} {
		if _, _, err := mem.Add(models.Event{
			Subject:    "urn:alaya:person:user:garden",
			Predicate:  "lives_in",
			Object:     stamp,
			ObservedAt: stamp,
		}); err != nil {
			t.Fatalf("写入: %v", err)
		}
	}

	entries := mem.Timeline("urn:alaya:person:user:garden", "")
	if len(entries) != 2 {
		t.Fatalf("应返回 2 条，实际 %d", len(entries))
	}

	if entries[0].Event.ObservedAt > entries[1].Event.ObservedAt {
		t.Fatal("时间线应按现实时间升序")
	}
}

// 省略 ObservedAt 是 MCP 调用方的默认行为。此时后写入的值必须成为当前值——
// 若判定退化为"先遍历到者胜出"，次序会与写入顺序完全相反，且失败是静默的。
func TestCurrentValueFollowsWriteOrderWithoutObservedAt(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:drift"

	for _, object := range []string{"深圳", "香港"} {
		if _, _, err := mem.Add(models.Event{Subject: subject, Predicate: "lives_in", Object: object}); err != nil {
			t.Fatalf("写入 %s: %v", object, err)
		}
	}

	current := currentObjects(mem.Timeline(subject, ""))

	if !current["香港"] {
		t.Fatal("后写入的值应为当前值")
	}
	if current["深圳"] {
		t.Fatal("先写入的值不应为当前值")
	}
}

// 时间戳精度不足（同一秒内多次写入）时，必须由链上序号给出确定次序。
func TestSameTimestampResolvedBySequence(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:drift"
	stamp := "2026-09-14T10:00:00+08:00"

	for _, object := range []string{"深圳", "香港"} {
		if _, _, err := mem.Add(models.Event{
			Subject: subject, Predicate: "lives_in", Object: object, ObservedAt: stamp,
		}); err != nil {
			t.Fatalf("写入 %s: %v", object, err)
		}
	}

	current := currentObjects(mem.Timeline(subject, ""))

	if !current["香港"] {
		t.Fatal("同一时间戳时，序号更大者应为当前值")
	}
}

// supersedes 的效力不依赖调用方是否显式给出 ObservedAt。
func TestSupersedeWorksWithoutObservedAt(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:drift"

	stale, _, err := mem.Add(models.Event{Subject: subject, Predicate: "lives_in", Object: "深圳"})
	if err != nil {
		t.Fatalf("写入深圳: %v", err)
	}

	if _, _, err := mem.Add(models.Event{Subject: subject, Predicate: "lives_in", Object: "香港"}); err != nil {
		t.Fatalf("写入香港: %v", err)
	}

	if _, _, err := mem.Add(models.Event{
		Subject: subject, Predicate: "lives_in", Object: "上海", Supersedes: stale.ID,
	}); err != nil {
		t.Fatalf("写入上海: %v", err)
	}

	current := currentObjects(mem.Timeline(subject, ""))

	if !current["上海"] {
		t.Fatal("最后写入的值应为当前值")
	}
	if current["深圳"] {
		t.Fatal("被 supersede 的旧值不应为当前值")
	}
}

// currentObjects 汇总哪些取值被判定为当前值。
func currentObjects(entries []Entry) map[string]bool {
	current := make(map[string]bool, len(entries))

	for _, entry := range entries {
		if entry.IsCurrent {
			current[entry.Event.Object] = true
		}
	}

	return current
}

// observed_at 接受 RFC3339，调用方可传任意偏移；
// 同为 +08:00 的对照组一直正确，跨偏移才暴露问题。
func TestCurrentValueAcrossTimezones(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:tz"

	// 先写入实际更晚的一条：02:00Z
	if _, _, err := mem.Add(models.Event{
		Subject:    subject,
		Predicate:  "departs_at",
		Object:     "flight-A(02:00Z)",
		ObservedAt: "2026-09-14T02:00:00+00:00",
	}); err != nil {
		t.Fatalf("写入 A: %v", err)
	}

	// 再写入实际更早的一条：09:00+08:00 即 01:00Z
	// 其字典序更大，若按字符串比较会被误判为"更晚"
	if _, _, err := mem.Add(models.Event{
		Subject:    subject,
		Predicate:  "departs_at",
		Object:     "flight-B(01:00Z)",
		ObservedAt: "2026-09-14T09:00:00+08:00",
	}); err != nil {
		t.Fatalf("写入 B: %v", err)
	}

	current := currentObjects(mem.Timeline(subject, ""))

	if !current["flight-A(02:00Z)"] {
		t.Fatal("02:00Z 才是更晚的时刻，应为当前值")
	}
	if current["flight-B(01:00Z)"] {
		t.Fatal("09:00+08:00 实际更早（01:00Z），不应为当前值")
	}
}

// 缺省写入的 ObservedAt 取自本地时间的 RecordedAt，
// 与显式传 UTC 的条目混排是常态，而非刻意混用偏移。
func TestMixedExplicitAndDefaultObservedAt(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:mixed"

	if _, _, err := mem.Add(models.Event{Subject: subject, Predicate: "p", Object: "缺省"}); err != nil {
		t.Fatalf("写入缺省条目: %v", err)
	}

	earlier := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339)
	if _, _, err := mem.Add(models.Event{
		Subject: subject, Predicate: "p", Object: "显式更早", ObservedAt: earlier,
	}); err != nil {
		t.Fatalf("写入显式条目: %v", err)
	}

	current := currentObjects(mem.Timeline(subject, ""))

	if !current["缺省"] {
		t.Fatal("缺省写入的是当前时刻，应为当前值")
	}
	if current["显式更早"] {
		t.Fatal("一小时前的时刻不应为当前值")
	}
}

// 时间线必须按时刻排序，而不是按字符串。
func TestTimelineOrdersAcrossTimezones(t *testing.T) {
	mem := newTestMemory(t)
	subject := "urn:alaya:test:order"

	stamps := []string{
		"2026-09-14T09:00:00+08:00", // 01:00Z
		"2026-09-14T02:00:00+00:00", // 02:00Z
	}

	for _, stamp := range stamps {
		if _, _, err := mem.Add(models.Event{
			Subject: subject, Predicate: "p", Object: stamp, ObservedAt: stamp,
		}); err != nil {
			t.Fatalf("写入 %s: %v", stamp, err)
		}
	}

	entries := mem.Timeline(subject, "")
	if len(entries) != 2 {
		t.Fatalf("应有 2 条，实际 %d", len(entries))
	}

	if first, second := entries[0].Event.ObservedAt, entries[1].Event.ObservedAt; first != stamps[0] || second != stamps[1] {
		t.Fatalf("应按时刻升序（01:00Z 在前），实际 %s, %s", first, second)
	}
}

// 读取端容忍畸形时间戳，不应成为写入端放行的理由——后者必须报错且不落盘。
func TestAddRejectsMalformedObservedAt(t *testing.T) {
	mem := newTestMemory(t)

	_, _, err := mem.Add(models.Event{
		Subject:    "urn:alaya:test:bad-time",
		Predicate:  "p",
		Object:     "v",
		ObservedAt: "2026-13-45T99:99:99+08:00",
	})

	if err == nil {
		t.Fatal("畸形时间戳应被拒绝")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("应返回可纠正的参数错误，实际: %v", err)
	}
	if got := len(mem.Events()); got != 0 {
		t.Fatalf("被拒绝的写入不应落盘，实际有 %d 条", got)
	}
}
