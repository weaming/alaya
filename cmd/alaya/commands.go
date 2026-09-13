package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/weaming/alaya/bedrock"
	"github.com/weaming/alaya/config"
	"github.com/weaming/alaya/memory"
	"github.com/weaming/alaya/models"
)

const indexFileName = "INDEX.md"

func runClaim(args []string) {
	flags := flag.NewFlagSet("claim", flag.ExitOnError)
	subject := flags.String("subject", "", "断言的主体")
	predicate := flags.String("predicate", "", "受控谓词，描述主体与客体之间的关系")
	object := flags.String("object", "", "断言的值或客体")
	supersedes := flags.String("supersedes", "", "被本条更新取代的旧记录 id")
	observedAt := flags.String("observed-at", "", "该断言在现实中成立的时间（RFC3339）")
	note := flags.String("note", "", "补充说明或原文出处")
	scope := flags.String("scope", models.DefaultScope, "声称者所在的 scope")
	world := flags.String("world", models.DefaultWorld, "真值所在的世界")
	parseArgs(flags, args)

	if *subject == "" || *predicate == "" {
		log.Fatal("用法: alaya claim --subject <主体> --predicate <谓词> --object <值>")
	}

	mem := openMemory()
	defer mem.Close()

	stored, isNew, err := mem.Add(models.Event{
		Subject:    *subject,
		Predicate:  *predicate,
		Object:     *object,
		Supersedes: *supersedes,
		ObservedAt: *observedAt,
		Note:       *note,
		Scope:      *scope,
		World:      *world,
	})
	if err != nil {
		log.Fatalf("写入记忆: %v", err)
	}

	if !isNew {
		fmt.Printf("该断言已存在，未重复写入。\nid: %s\n", stored.ID)
		return
	}

	fmt.Printf("已记录。\nid: %s\n", stored.ID)
}

func runSearch(args []string) {
	flags := flag.NewFlagSet("search", flag.ExitOnError)
	query := flags.String("query", "", "检索词，支持中英文")
	limit := flags.Int("limit", 10, "返回条数上限")
	scope := flags.String("scope", "", "仅返回该 scope 内的记忆；缺省不过滤")
	world := flags.String("world", "", "仅返回该世界内的记忆；缺省不过滤")
	positional := parseArgs(flags, args)

	// 位置参数回退：alaya search <关键词> 比 --query 更自然
	target := *query
	if target == "" {
		target = strings.Join(positional, " ")
	}

	if target == "" {
		log.Fatal("用法: alaya search <关键词>")
	}

	mem := openMemory()
	defer mem.Close()

	entries := mem.SearchIn(target, *limit, *world, *scope)
	if len(entries) == 0 {
		fmt.Println("没有找到匹配的记忆。")
		return
	}

	for _, entry := range entries {
		fmt.Printf("%s\n  id: %s  相关度: %.3f\n", entry.Format(), entry.Event.ID, entry.Score)
	}
}

func runHistory(args []string) {
	flags := flag.NewFlagSet("history", flag.ExitOnError)
	subject := flags.String("subject", "", "要追溯的主体")
	predicate := flags.String("predicate", "", "限定谓词，只看某个维度的演变")
	scope := flags.String("scope", "", "仅追溯该 scope 内的演变；缺省不过滤")
	world := flags.String("world", "", "仅追溯该世界内的演变；缺省不过滤")
	positional := parseArgs(flags, args)

	// 位置参数回退：alaya history <主体> 比 --subject 更自然
	target := *subject
	if target == "" {
		target = strings.Join(positional, " ")
	}

	if target == "" && *predicate == "" && *scope == "" && *world == "" {
		log.Fatal("用法: alaya history <主体>，或用 --predicate / --scope / --world 限定范围")
	}

	mem := openMemory()
	defer mem.Close()

	entries := mem.TimelineIn(target, *predicate, *world, *scope)
	if len(entries) == 0 {
		fmt.Println("没有找到该主体的记忆。")
		return
	}

	fmt.Printf("## %s 的记忆（%d 条）\n\n", historyTitle(target, *predicate, *world, *scope), len(entries))
	for _, entry := range entries {
		fmt.Printf("%s\n  id: %s\n", entry.Format(), entry.Event.ID)
	}
}

func runStats(args []string) {
	flags := flag.NewFlagSet("stats", flag.ExitOnError)
	scope := flags.String("scope", "", "仅统计该 scope；缺省统计全部")
	world := flags.String("world", "", "仅统计该世界；缺省统计全部")
	parseArgs(flags, args)

	mem := openMemory()
	defer mem.Close()

	scopes := make(map[string]struct{})
	subjects := make(map[string]struct{})

	var (
		events     []models.Event
		current    int
		historical int
		earliest   models.Event
		latest     models.Event
		hasLatest  bool
	)

	// 单次遍历同时完成过滤与归类，避免为 scope 过滤再扫一遍全量
	for _, entry := range mem.Annotated() {
		if *world != "" && entry.Event.World != *world {
			continue
		}
		if *scope != "" && entry.Event.Scope != *scope {
			continue
		}

		events = append(events, entry.Event)
		scopes[entry.Event.Scope] = struct{}{}
		subjects[entry.Event.Subject] = struct{}{}

		if entry.IsCurrent {
			current++
		} else {
			historical++
		}

		if !hasLatest || models.CompareObserved(entry.Event, earliest) < 0 {
			earliest = entry.Event
		}
		if !hasLatest || models.CompareObserved(entry.Event, latest) > 0 {
			latest = entry.Event
		}
		hasLatest = true
	}

	if len(events) == 0 {
		fmt.Println("该范围内没有记忆。")
		return
	}

	fmt.Printf("数据目录: %s\n", mem.Dir())

	if info, err := os.Stat(filepath.Join(mem.Dir(), "events.jsonl")); err == nil {
		fmt.Printf("日志大小: %.1f KB\n", float64(info.Size())/1024)
	}

	fmt.Printf("事件总数: %d\n", len(events))
	fmt.Printf("  当前值: %d\n", current)
	fmt.Printf("  历史值: %d\n", historical)
	fmt.Printf("scope 数: %d\n", len(scopes))
	fmt.Printf("主体数: %d\n", len(subjects))

	if hasLatest {
		fmt.Printf("时间跨度: %s ~ %s\n",
			memory.ShortTime(earliest.EffectiveObservedAt()),
			memory.ShortTime(latest.EffectiveObservedAt()))
	}
}

func runVerify() {
	dir, err := config.DataDir()
	if err != nil {
		log.Fatalf("定位数据目录: %v", err)
	}

	store, err := bedrock.Open(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "校验失败：%v\n", err)
		os.Exit(1)
	}
	defer store.Close()

	fmt.Printf("校验通过：%d 条事件，内容哈希与链哈希均一致。\n", len(store.Events()))

	switch {
	case store.TruncatedTail():
		fmt.Println("注意：日志尾部存在未写完的半行，已自动截断（通常由进程崩溃导致）。")
	case store.TailPending():
		fmt.Println("注意：日志尾部存在未写完的半行，将在下次写入时修复（通常由进程崩溃导致）。")
	}
}

func runRebuild() {
	mem := openMemory()
	defer mem.Close()

	path := filepath.Join(mem.Dir(), indexFileName)
	if err := writeIndex(mem, path); err != nil {
		log.Fatalf("重建索引: %v", err)
	}

	fmt.Printf("已重建 %s\n", path)
}

// writeIndex 从事件日志重建人类可读的索引文件。
//
// 它是纯粹的派生物：删掉也不损失任何信息，随时可由真源重建。
func writeIndex(mem *memory.Memory, path string) error {
	events := mem.Events()
	current, historical := mem.SplitByCurrency()

	sortBySubject(current)
	sortBySubject(historical)

	var builder strings.Builder
	builder.WriteString("# Alaya 记忆索引\n\n")
	builder.WriteString("> 本文件是派生物，可由 `alaya rebuild` 随时重建。真源是 `events.jsonl`。\n\n")
	fmt.Fprintf(&builder, "生成时间：%s\n\n", config.NowISO())
	fmt.Fprintf(&builder, "共 %d 条记录：当前值 %d 条，历史值 %d 条。\n\n", len(events), len(current), len(historical))

	builder.WriteString("## 当前值\n\n")
	writeIndexEntries(&builder, current)

	if len(historical) > 0 {
		builder.WriteString("\n## 历史值\n\n")
		writeIndexEntries(&builder, historical)
	}

	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return fmt.Errorf("写入索引文件: %w", err)
	}

	return nil
}

// historyTitle 给演变列表起个能说明「看的是什么范围」的标题。
func historyTitle(subject, predicate, world, scope string) string {
	switch {
	case subject != "":
		return subject
	case predicate != "":
		return predicate
	case scope != "":
		return scope
	default:
		return world
	}
}

func sortBySubject(entries []memory.Entry) {
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Event.Subject != entries[j].Event.Subject {
			return entries[i].Event.Subject < entries[j].Event.Subject
		}
		if entries[i].Event.Predicate != entries[j].Event.Predicate {
			return entries[i].Event.Predicate < entries[j].Event.Predicate
		}

		return models.CompareObserved(entries[i].Event, entries[j].Event) > 0
	})
}

func writeIndexEntries(builder *strings.Builder, entries []memory.Entry) {
	var lastSubject string

	for _, entry := range entries {
		if entry.Event.Subject != lastSubject {
			fmt.Fprintf(builder, "### %s\n\n", entry.Event.Subject)
			lastSubject = entry.Event.Subject
		}

		line := fmt.Sprintf("- **%s**: %s（记于 %s）",
			entry.Event.Predicate, entry.Event.Object, memory.ShortTime(entry.Event.EffectiveObservedAt()))
		if entry.Event.Note != "" {
			line += " — " + entry.Event.Note
		}

		builder.WriteString(line + "\n")
	}
}
