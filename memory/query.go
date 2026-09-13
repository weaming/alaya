package memory

import (
	"sort"

	"github.com/weaming/alaya/models"
)

const (
	defaultBudgetTokens = 800
	recallCandidates    = 60
	historicalWeight    = 0.6
	maxPerTriple        = 3
)

// Search 按关键词检索并标注每条结果在断言组中的地位。
func (m *Memory) Search(query string, limit int) []Entry {
	return m.SearchIn(query, limit, "", "")
}

// SearchIn 在给定 world 与 scope 内检索；空值表示不过滤。
func (m *Memory) SearchIn(query string, limit int, world, scope string) []Entry {
	// 过滤发生在检索之后，故先多取候选再截断
	candidateLimit := limit
	if world != "" || scope != "" {
		candidateLimit = limit * 3
	}

	hits := m.index.Search(query, candidateLimit)

	events := make([]models.Event, 0, len(hits))
	scores := make(map[string]float64, len(hits))
	for _, hit := range hits {
		if world != "" && hit.Event.World != world {
			continue
		}
		if scope != "" && hit.Event.Scope != scope {
			continue
		}

		events = append(events, hit.Event)
		scores[hit.Event.ID] = hit.Score
	}

	if limit > 0 && len(events) > limit {
		events = events[:limit]
	}

	return m.annotate(events, scores)
}

// Timeline 返回某主体或某谓词维度的完整历史，按现实时间升序。
// 两个条件都为空时返回空结果——调用方应至少给出一个。
func (m *Memory) Timeline(subject, predicate string) []Entry {
	if subject == "" && predicate == "" {
		return nil
	}

	matched := make([]models.Event, 0)
	for _, event := range m.store.Events() {
		if subject != "" && event.Subject != subject {
			continue
		}
		if predicate != "" && event.Predicate != predicate {
			continue
		}

		matched = append(matched, event)
	}

	sort.SliceStable(matched, func(i, j int) bool {
		if order := models.CompareObserved(matched[i], matched[j]); order != 0 {
			return order < 0
		}

		return matched[i].Seq < matched[j].Seq
	})

	return m.annotate(matched, nil)
}

// RecallResult 是一次上下文装配的结果。
type RecallResult struct {
	Entries   []Entry
	Truncated int
	Spent     int
	Budget    int
}

// Recall 装配预算受限的上下文。
//
// 装配规则对应 05 §6.2：同一断言的多个值必须显式标注哪条是当前值。
// 把新旧值不加标记地并列，会让生成器取平均、追问或挑旧的。
func (m *Memory) Recall(query string, budget int) RecallResult {
	if budget <= 0 {
		budget = defaultBudgetTokens
	}

	hits := m.index.Search(query, recallCandidates)
	if len(hits) == 0 {
		return RecallResult{Budget: budget}
	}

	known := m.resolveState()

	entries := make([]Entry, 0, len(hits))
	for _, hit := range hits {
		isCurrent := known.current[hit.Event.ID]

		score := hit.Score
		if !isCurrent {
			score *= historicalWeight
		}

		entries = append(entries, Entry{
			Event:        hit.Event,
			IsCurrent:    isCurrent,
			SupersededBy: known.supersededBy[hit.Event.ID],
			Score:        score,
		})
	}

	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].Score != entries[j].Score {
			return entries[i].Score > entries[j].Score
		}

		return isLater(entries[i].Event, entries[j].Event)
	})

	return fill(entries, budget)
}

// fill 按预算装填条目，并对同一断言组限制条数，避免历史值淹没预算。
func fill(entries []Entry, budget int) RecallResult {
	var (
		result RecallResult
		spent  int
		seen   = make(map[models.Triple]int)
	)

	result.Budget = budget

	for _, entry := range entries {
		key := entry.Event.Triple()
		if seen[key] >= maxPerTriple {
			result.Truncated++
			continue
		}

		cost := estimateTokens(entry.Format())
		if spent+cost > budget {
			result.Truncated++
			continue
		}

		spent += cost
		seen[key]++
		result.Entries = append(result.Entries, entry)
	}

	result.Spent = spent

	return result
}
