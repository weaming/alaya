package bedrock

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weaming/alaya/models"
)

func openTestStore(t *testing.T, dir string) *Store {
	t.Helper()

	store, err := Open(dir)
	if err != nil {
		t.Fatalf("打开存储: %v", err)
	}

	return store
}

func TestAppendSurvivesReload(t *testing.T) {
	dir := t.TempDir()

	store := openTestStore(t, dir)
	event, isNew, err := store.Append(models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "prefers",
		Object:     "hand_drip",
		ObservedAt: "2026-09-14T10:00:00+08:00",
	})
	if err != nil {
		t.Fatalf("追加事件: %v", err)
	}
	if !isNew {
		t.Fatal("首次写入应报告为新建")
	}
	if event.Seq != 1 || event.ID == "" || event.Link == "" {
		t.Fatalf("事件缺少 id/link/seq: %+v", event)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	reopened := openTestStore(t, dir)
	defer reopened.Close()

	events := reopened.Events()
	if len(events) != 1 {
		t.Fatalf("重新加载得到 %d 条事件，期望 1 条", len(events))
	}
	if events[0].ID != event.ID {
		t.Fatalf("重新加载后 id 不匹配: %s != %s", events[0].ID, event.ID)
	}
}

func TestAppendIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t, dir)
	defer store.Close()

	input := models.Event{
		Subject:    "urn:alaya:person:user:garden",
		Predicate:  "prefers",
		Object:     "hand_drip",
		ObservedAt: "2026-09-14T10:00:00+08:00",
	}

	first, _, err := store.Append(input)
	if err != nil {
		t.Fatalf("首次追加: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*models.Event)
	}{
		{"完全相同", func(*models.Event) {}},
		{"时间戳不同", func(e *models.Event) { e.ObservedAt = "2026-09-15T10:00:00+08:00" }},
		{"来源不同", func(e *models.Event) { e.AttributedBy = "urn:alaya:person:other" }},
		{"备注不同", func(e *models.Event) { e.Note = "补记" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := input
			test.mutate(&event)

			second, isNew, err := store.Append(event)
			if err != nil {
				t.Fatalf("重复追加: %v", err)
			}
			if isNew {
				t.Fatal("内容相同的写入不应新建记录")
			}
			if second.ID != first.ID {
				t.Fatalf("去重后返回的 id 不一致: %s != %s", second.ID, first.ID)
			}
		})
	}

	if got := len(store.Events()); got != 1 {
		t.Fatalf("事件数应为 1，实际 %d", got)
	}
}

func TestChainIsPerScope(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t, dir)
	defer store.Close()

	first, _, err := store.Append(models.Event{Scope: "user:a", Subject: "s", Predicate: "p", Object: "1"})
	if err != nil {
		t.Fatalf("追加: %v", err)
	}

	second, _, err := store.Append(models.Event{Scope: "user:a", Subject: "s", Predicate: "p", Object: "2"})
	if err != nil {
		t.Fatalf("追加: %v", err)
	}

	other, _, err := store.Append(models.Event{Scope: "user:b", Subject: "s", Predicate: "p", Object: "1"})
	if err != nil {
		t.Fatalf("追加: %v", err)
	}

	if second.Seq != first.Seq+1 {
		t.Fatalf("同 scope 内序号应为 %d，实际 %d", first.Seq+1, second.Seq)
	}
	if second.Prev != first.Link {
		t.Fatal("同 scope 内应串联前一条的链哈希")
	}

	if other.Seq != 1 || other.Prev != "" {
		t.Fatalf("不同 scope 的链应相互独立，实际 seq=%d prev=%q", other.Seq, other.Prev)
	}
}

// 无锁加载不能截断残行：那半行可能是另一个进程正在写的，
// 截断它会破坏对方的数据。这里只忽略并记为待修复。
func TestLoadIgnoresPartialTailWithoutRepair(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, eventsFileName)

	store := openTestStore(t, dir)
	if _, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: "o"}); err != nil {
		t.Fatalf("追加: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	// 模拟进程在写入途中崩溃，留下半行
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开日志: %v", err)
	}
	if _, err := file.WriteString(`{"v":1,"id":"sha256:broken`); err != nil {
		t.Fatalf("写入残行: %v", err)
	}
	file.Close()

	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取日志状态: %v", err)
	}

	reopened := openTestStore(t, dir)
	defer reopened.Close()

	if got := len(reopened.Events()); got != 1 {
		t.Fatalf("残行应被忽略，事件数应为 1，实际 %d", got)
	}
	if !reopened.TailPending() {
		t.Fatal("应标记尾部待修复")
	}

	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取日志状态: %v", err)
	}
	if before.Size() != after.Size() {
		t.Fatalf("无锁加载不应改动文件：%d → %d", before.Size(), after.Size())
	}
}

// 残行必须在锁内修掉，否则新事件会接在半行之后，
// 与它拼成一行无法解析的内容，整份日志随之失效。
func TestAppendRepairsPartialTail(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, eventsFileName)

	store := openTestStore(t, dir)
	if _, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: "o"}); err != nil {
		t.Fatalf("追加: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("打开日志: %v", err)
	}
	if _, err := file.WriteString(`{"v":1,"id":"sha256:broken`); err != nil {
		t.Fatalf("写入残行: %v", err)
	}
	file.Close()

	reopened := openTestStore(t, dir)
	if _, _, err := reopened.Append(models.Event{Subject: "s", Predicate: "p", Object: "o2"}); err != nil {
		t.Fatalf("续写: %v", err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	verify := openTestStore(t, dir)
	defer verify.Close()

	events := verify.Events()
	if len(events) != 2 {
		t.Fatalf("应有 2 条事件，实际 %d", len(events))
	}

	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("第 %d 条序号应为 %d，实际 %d", i+1, i+1, event.Seq)
		}
	}
}

// 只读挂载、备份副本、容器只读卷下，查询与校验必须可用——
// 它们不需要写权限，不应因无法创建锁文件而失效。
func TestOpenReadOnlyDirectory(t *testing.T) {
	dir := t.TempDir()
	lockPath := filepath.Join(dir, lockFileName)

	store := openTestStore(t, dir)
	if _, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: "o"}); err != nil {
		t.Fatalf("写入: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	if err := os.Remove(lockPath); err != nil {
		t.Fatalf("删除锁文件: %v", err)
	}
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatalf("设为只读: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })

	readOnly := openTestStore(t, dir)
	defer readOnly.Close()

	if got := len(readOnly.Events()); got != 1 {
		t.Fatalf("只读加载应看到 1 条事件，实际 %d", got)
	}

	// 读命令不得创建锁文件
	if _, err := os.Stat(lockPath); err == nil {
		t.Fatal("只读加载不应创建锁文件")
	}

	// 写入必须明确失败，而不是静默或崩溃
	if _, _, err := readOnly.Append(models.Event{Subject: "s", Predicate: "p", Object: "o2"}); err == nil {
		t.Fatal("只读目录下写入应报错")
	}
}

// 无锁加载与并发写入交叠时，若指纹描述了比已解析内容更新的文件状态，
// 后续写入会误判为"未变化"而跳过重载，沿用过期链尾追加，写出重复序号。
//
// 这是低概率时序（实测 9 次中 1 次），压力测试尽力覆盖：反复以新实例加载，
// 同时有写者持续追加，使 stat 与 read 之间尽可能落入一次写入。
func TestConcurrentOpenDuringWrites(t *testing.T) {
	const (
		warmup       = 300
		rounds       = 30
		writersEach  = 6
		scope        = "user:a"
		testSubject  = "urn:alaya:test:race"
		warmupObject = "seed"
	)

	dir := t.TempDir()

	// 预热：让无锁加载的耗时足以覆盖一次写入
	seed, err := Open(dir)
	if err != nil {
		t.Fatalf("预热加载: %v", err)
	}
	for i := 0; i < warmup; i++ {
		if _, _, err := seed.Append(models.Event{
			Scope: scope, Subject: testSubject, Predicate: warmupObject, Object: fmt.Sprintf("v%d", i),
		}); err != nil {
			t.Fatalf("预热写入: %v", err)
		}
	}
	if err := seed.Close(); err != nil {
		t.Fatalf("关闭预热存储: %v", err)
	}

	writer, err := Open(dir)
	if err != nil {
		t.Fatalf("加载长命写者: %v", err)
	}
	defer writer.Close()

	for round := 0; round < rounds; round++ {
		var wait sync.WaitGroup

		for i := 0; i < writersEach; i++ {
			id := round*writersEach + i

			// 短命写者：加载（无锁）后立即写
			wait.Add(1)

			go func(id int) {
				defer wait.Done()

				store, err := Open(dir)
				if err != nil {
					t.Errorf("短命写者加载: %v", err)
					return
				}
				defer store.Close()

				if _, _, err := store.Append(models.Event{
					Scope: scope, Subject: testSubject, Predicate: "short", Object: fmt.Sprintf("v%d", id),
				}); err != nil {
					t.Errorf("短命写者写入: %v", err)
				}
			}(id)

			// 长命写者：同时追加，制造 stat 与 read 之间的写入
			wait.Add(1)

			go func(id int) {
				defer wait.Done()

				if _, _, err := writer.Append(models.Event{
					Scope: scope, Subject: testSubject, Predicate: "long", Object: fmt.Sprintf("v%d", id),
				}); err != nil {
					t.Errorf("长命写者写入: %v", err)
				}
			}(id)
		}

		wait.Wait()
	}

	verify, err := Open(dir)
	if err != nil {
		t.Fatalf("校验加载: %v", err)
	}
	defer verify.Close()

	events := verify.Events()
	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("第 %d 条序号应为 %d，实际 %d（链已断裂）", i+1, i+1, event.Seq)
		}
	}

	want := warmup + rounds*writersEach*2
	if len(events) != want {
		t.Fatalf("应有 %d 条事件，实际 %d", want, len(events))
	}
}

// 中间损坏与尾部残行必须区别对待：前者是篡改，绝不能静默丢弃其后内容。
func TestLoadRejectsCorruptionInMiddle(t *testing.T) {
	dir := t.TempDir()

	store := openTestStore(t, dir)
	for _, object := range []string{"1", "2"} {
		if _, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: object}); err != nil {
			t.Fatalf("追加: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	path := filepath.Join(dir, eventsFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}

	lines := strings.SplitAfter(string(raw), "\n")
	lines[0] = "{not json}\n"
	if err := os.WriteFile(path, []byte(strings.Join(lines, "")), 0o644); err != nil {
		t.Fatalf("写入损坏内容: %v", err)
	}

	if _, err := Open(dir); err == nil {
		t.Fatal("中间行损坏应报错，而不是静默截断其后内容")
	}
}

func TestLoadDetectsTampering(t *testing.T) {
	dir := t.TempDir()

	store := openTestStore(t, dir)
	if _, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: "hand_drip"}); err != nil {
		t.Fatalf("追加: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	path := filepath.Join(dir, eventsFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}

	// 等长替换：JSON 仍可解析，但内容哈希必然不匹配
	tampered := strings.Replace(string(raw), "hand_drip", "espresso_", 1)
	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("写入篡改内容: %v", err)
	}

	if _, err := Open(dir); err == nil {
		t.Fatal("篡改应被检测到")
	}
}

func TestLoadDetectsBrokenChain(t *testing.T) {
	dir := t.TempDir()

	store := openTestStore(t, dir)
	for _, object := range []string{"1", "2"} {
		if _, _, err := store.Append(models.Event{Scope: "user:a", Subject: "s", Predicate: "p", Object: object}); err != nil {
			t.Fatalf("追加: %v", err)
		}
	}
	if err := store.Close(); err != nil {
		t.Fatalf("关闭存储: %v", err)
	}

	// 删除中间一行：链必然断裂
	path := filepath.Join(dir, eventsFileName)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}

	lines := strings.SplitAfter(string(raw), "\n")
	if err := os.WriteFile(path, []byte(strings.Join(lines[1:], "")), 0o644); err != nil {
		t.Fatalf("写入截断内容: %v", err)
	}

	if _, err := Open(dir); err == nil {
		t.Fatal("链断裂应被检测到")
	}
}

// 两个 Store 实例代表两个进程（长驻的 MCP server 与 CLI）。
// 后者写入后，前者的内存链尾即告失效，必须在写入前察觉并重载——
// 否则会写出重复序号，而链校验失败会让整份日志拒绝加载。
func TestConcurrentWritersDoNotBreakChain(t *testing.T) {
	dir := t.TempDir()
	scope := "user:a"

	first := openTestStore(t, dir)
	defer first.Close()

	if _, _, err := first.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "1"}); err != nil {
		t.Fatalf("第一个写者: %v", err)
	}

	// 第二个写者：独立进程，重新加载链尾
	second := openTestStore(t, dir)
	if _, _, err := second.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "2"}); err != nil {
		t.Fatalf("第二个写者: %v", err)
	}
	if err := second.Close(); err != nil {
		t.Fatalf("关闭第二个写者: %v", err)
	}

	// 第一个写者续写：它的内存链尾已过期
	if _, _, err := first.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "3"}); err != nil {
		t.Fatalf("第一个写者续写: %v", err)
	}

	verify := openTestStore(t, dir)
	defer verify.Close()

	events := verify.Events()
	if len(events) != 3 {
		t.Fatalf("应有 3 条事件，实际 %d", len(events))
	}

	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("第 %d 条序号应为 %d，实际 %d", i+1, i+1, event.Seq)
		}
	}
}

// 数据目录在进程存活期间被外部改动（删库重来、从备份恢复）后，
// 该进程的续写必须写出合法链，而不是沿用已失效的内存链尾。
func TestWriteAfterExternalTruncation(t *testing.T) {
	dir := t.TempDir()
	scope := "user:a"

	store := openTestStore(t, dir)
	defer store.Close()

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "1"}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, eventsFileName), nil, 0o644); err != nil {
		t.Fatalf("清空日志: %v", err)
	}

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "2"}); err != nil {
		t.Fatalf("清空后续写: %v", err)
	}

	reopened := openTestStore(t, dir)
	defer reopened.Close()

	events := reopened.Events()
	if len(events) != 1 {
		t.Fatalf("清空后应只有 1 条事件，实际 %d", len(events))
	}
	if events[0].Seq != 1 {
		t.Fatalf("清空后的首条应为 seq 1，实际 %d", events[0].Seq)
	}
}

// 省略 ObservedAt 时，写入路径应把它补成 RecordedAt，
// 使日志自描述——否则读侧的排序与当前值判定都失去依据。
func TestAppendFillsObservedAt(t *testing.T) {
	dir := t.TempDir()
	store := openTestStore(t, dir)
	defer store.Close()

	event, _, err := store.Append(models.Event{Subject: "s", Predicate: "p", Object: "o"})
	if err != nil {
		t.Fatalf("写入: %v", err)
	}

	if event.ObservedAt == "" {
		t.Fatal("ObservedAt 应被补齐，不能落盘为空串")
	}
	if event.ObservedAt != event.RecordedAt {
		t.Fatalf("未指定时应回退到 RecordedAt，实际 observed=%q recorded=%q",
			event.ObservedAt, event.RecordedAt)
	}
}

// 同一进程内的并发写入由 Store.mu 串行化；跨进程则由文件锁负责。
// 两者都失效时，序号会重复，整份日志将无法通过校验。
func TestConcurrentAppendsWithinProcess(t *testing.T) {
	const (
		writers    = 8
		perWriter  = 10
		totalCount = writers * perWriter
	)

	dir := t.TempDir()
	store := openTestStore(t, dir)
	defer store.Close()

	var wait sync.WaitGroup

	for writer := 0; writer < writers; writer++ {
		wait.Add(1)

		go func(id int) {
			defer wait.Done()

			for i := 0; i < perWriter; i++ {
				if _, _, err := store.Append(models.Event{
					Scope:     "user:a",
					Subject:   "s",
					Predicate: "p",
					Object:    fmt.Sprintf("w%d-%d", id, i),
				}); err != nil {
					t.Errorf("并发写入: %v", err)
					return
				}
			}
		}(writer)
	}

	wait.Wait()

	reopened := openTestStore(t, dir)
	defer reopened.Close()

	events := reopened.Events()
	if len(events) != totalCount {
		t.Fatalf("应有 %d 条事件，实际 %d", totalCount, len(events))
	}

	for i, event := range events {
		if event.Seq != int64(i+1) {
			t.Fatalf("第 %d 条序号应为 %d，实际 %d", i+1, i+1, event.Seq)
		}
	}
}

// 等长重写不改变尺寸，只有修改时间能暴露它。
// 失效检测若只看尺寸，本进程会继续基于过期链尾追加。
func TestRefreshDetectsEqualLengthRewrite(t *testing.T) {
	dir := t.TempDir()
	scope := "user:a"
	path := filepath.Join(dir, eventsFileName)

	store := openTestStore(t, dir)
	defer store.Close()

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "1"}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("读取日志: %v", err)
	}

	tampered := strings.Replace(string(raw), `"object":"1"`, `"object":"2"`, 1)
	if len(tampered) != len(raw) {
		t.Fatalf("替换必须等长：%d → %d", len(raw), len(tampered))
	}

	if err := os.WriteFile(path, []byte(tampered), 0o644); err != nil {
		t.Fatalf("写入篡改内容: %v", err)
	}

	// 显式拉开修改时间，避免依赖文件系统的时间戳精度
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(path, future, future); err != nil {
		t.Fatalf("设置修改时间: %v", err)
	}

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "3"}); err == nil {
		t.Fatal("等长篡改后续写应报错，而不是基于过期状态继续追加")
	}

	// 损坏状态必须持续拒绝写入，不能偶尔放行
	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "4"}); err == nil {
		t.Fatal("损坏状态应持续拒绝写入")
	}

	// 但不应清空视图——那会让调用方误以为记忆没了
	if got := len(store.Events()); got != 1 {
		t.Fatalf("应保留损坏前的视图（1 条），实际 %d", got)
	}
}

// `.lock` 被删除后，本进程的锁作用于已被 unlink 的旧 inode，
// 与新进程创建的新 inode 互不相干。续写前必须复核并重建。
func TestLockFileRecreatedAfterDeletion(t *testing.T) {
	dir := t.TempDir()
	scope := "user:a"
	path := filepath.Join(dir, lockFileName)

	store := openTestStore(t, dir)
	defer store.Close()

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "0"}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	stale, err := os.Stat(path)
	if err != nil {
		t.Fatalf("读取锁文件: %v", err)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("删除锁文件: %v", err)
	}

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "1"}); err != nil {
		t.Fatalf("删除锁后续写: %v", err)
	}

	fresh, err := os.Stat(path)
	if err != nil {
		t.Fatalf("锁文件应被重建: %v", err)
	}
	if os.SameFile(stale, fresh) {
		t.Fatal("锁文件应被重新创建，而不是沿用已被 unlink 的旧 inode")
	}
}

// 替换（同路径、新 inode）与删除同类：原 fd 锁住的是旧对象。
// 这里直接验证身份复核本身——从外部只能看到路径上的同一个文件，
// 观察不到进程实际持有的是哪个 inode。
func TestLockFileIntactDetectsReplacement(t *testing.T) {
	dir := t.TempDir()
	scope := "user:a"
	path := filepath.Join(dir, lockFileName)

	store := openTestStore(t, dir)
	defer store.Close()

	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "0"}); err != nil {
		t.Fatalf("写入: %v", err)
	}

	if !store.lockFileIntact() {
		t.Fatal("未改动时锁文件应判定为完整")
	}

	// 以 rename 替换：路径不变，inode 变
	replacement := filepath.Join(dir, "lock.replacement")
	if err := os.WriteFile(replacement, nil, 0o644); err != nil {
		t.Fatalf("创建替代文件: %v", err)
	}
	if err := os.Rename(replacement, path); err != nil {
		t.Fatalf("替换锁文件: %v", err)
	}

	if store.lockFileIntact() {
		t.Fatal("锁文件被替换后应判定为失效")
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("删除锁文件: %v", err)
	}

	if store.lockFileIntact() {
		t.Fatal("锁文件被删除后应判定为失效")
	}

	// 复核失效之后，续写必须能自愈
	if _, _, err := store.Append(models.Event{Scope: scope, Subject: "s", Predicate: "p", Object: "1"}); err != nil {
		t.Fatalf("损坏后续写: %v", err)
	}
	if !store.lockFileIntact() {
		t.Fatal("重建后锁文件应重新判定为完整")
	}
}
