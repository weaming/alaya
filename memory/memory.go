// Package memory 是应用服务层：把基岩与检索编排成"记住、想起、追溯"三类操作。
//
// 它不引入任何新的真源——所有状态都可从基岩重建，
// 内存索引只是为了让查询不必每次扫描全量事件。
package memory

import (
	"errors"
	"fmt"

	"github.com/weaming/alaya/bedrock"
	"github.com/weaming/alaya/models"
	"github.com/weaming/alaya/search"
)

// ErrInvalidArgument 表示请求本身不合法，而非执行失败。
// 调用方（如 MCP 工具层）应据此把它作为可纠正的输入错误回报，
// 而不是当作系统故障。
var ErrInvalidArgument = errors.New("参数不合法")

// Memory 是记忆能力的入口。
type Memory struct {
	dir   string
	store *bedrock.Store
	index *search.Index
}

// Open 打开数据目录，校验基岩完整性并重建内存索引。
func Open(dir string) (*Memory, error) {
	store, err := bedrock.Open(dir)
	if err != nil {
		return nil, err
	}

	index := search.New()
	index.Build(store.Events())

	return &Memory{dir: dir, store: store, index: index}, nil
}

func (m *Memory) Close() error {
	return m.store.Close()
}

func (m *Memory) Dir() string {
	return m.dir
}

func (m *Memory) Events() []models.Event {
	return m.store.Events()
}

// Add 写入一条事件并同步内存索引。内容重复时返回已存在的记录，索引不变。
func (m *Memory) Add(event models.Event) (models.Event, bool, error) {
	// 校验取代目标确实存在：打错 id 会让更正在无声中失效，
	// 而用户会以为旧值已被更正——这正是"以为记住了、其实没有"的来源
	if event.Supersedes != "" && !m.store.Exists(event.Supersedes) {
		return models.Event{}, false, fmt.Errorf("%w: supersedes 指向的记录不存在: %s",
			ErrInvalidArgument, event.Supersedes)
	}

	if err := event.Validate(); err != nil {
		return models.Event{}, false, fmt.Errorf("%w: %s", ErrInvalidArgument, err)
	}

	stored, isNew, err := m.store.Append(event)
	if err != nil {
		return models.Event{}, false, err
	}

	if isNew {
		m.index.Add(stored)
	}

	return stored, isNew, nil
}

// Entry 是一条带状态标注的记忆条目。
type Entry struct {
	Event        models.Event
	IsCurrent    bool
	SupersededBy string
	Score        float64
}

// state 描述各断言组内条目的地位。
type state struct {
	current      map[string]bool   // 属于当前值的条目 id
	supersededBy map[string]string // 被取代者 id -> 取代者 id
}

// resolveState 判定每个断言组的当前值与取代关系。
//
// 判定必须在全量事件上进行，而不只是检索结果上——当前值可能没有命中查询，
// 若只看结果集，一条高分的旧值会被误标为当前值，而这正是
// "检索正确、信念错误"最常见的触发方式（12 §4.1）。
func (m *Memory) resolveState() state {
	events := m.store.Events()
	result := state{
		current:      make(map[string]bool),
		supersededBy: make(map[string]string),
	}

	for _, event := range events {
		if event.Supersedes != "" {
			result.supersededBy[event.Supersedes] = event.ID
		}
	}

	latest := make(map[models.Triple]models.Event)
	for _, event := range events {
		if _, isSuperseded := result.supersededBy[event.ID]; isSuperseded {
			continue
		}

		key := event.Triple()
		if prev, ok := latest[key]; !ok || isLater(event, prev) {
			latest[key] = event
		}
	}

	for _, event := range latest {
		result.current[event.ID] = true
	}

	return result
}

// isLater 判断 a 是否排在 b 之后。
//
// 先比有效现实时间，再以链上序号决胜：同一时刻的多次写入（或时间戳精度不足）
// 只有序号能给出确定次序，否则判定会退化为"先遍历到者胜出"。
func isLater(a, b models.Event) bool {
	if order := models.CompareObserved(a, b); order != 0 {
		return order > 0
	}

	return a.Seq > b.Seq
}

// annotate 给检索结果补上当前值标注。
func (m *Memory) annotate(events []models.Event, scores map[string]float64) []Entry {
	known := m.resolveState()

	entries := make([]Entry, 0, len(events))
	for _, event := range events {
		entries = append(entries, Entry{
			Event:        event,
			IsCurrent:    known.current[event.ID],
			SupersededBy: known.supersededBy[event.ID],
			Score:        scores[event.ID],
		})
	}

	return entries
}

// Annotated 返回全部事件及其当前值标注。
func (m *Memory) Annotated() []Entry {
	return m.annotate(m.store.Events(), nil)
}

// SplitByCurrency 把全部条目分为当前值与历史值两组，顺序与基岩中一致。
func (m *Memory) SplitByCurrency() (current, historical []Entry) {
	for _, entry := range m.Annotated() {
		if entry.IsCurrent {
			current = append(current, entry)
			continue
		}

		historical = append(historical, entry)
	}

	return current, historical
}
