// Package search 提供零依赖的倒排索引与 BM25 打分。
//
// 拉丁文按词切分、CJK 按 bigram 切分：无需词典，对中英混排天然有效，
// 且不引入分词库依赖。检索被刻意做得简单——外部实测显示，
// 投在写入侧的组织质量比投在读取侧的检索技巧回报更高（12 §3.1）。
package search

import (
	"math"
	"sort"
	"strings"
	"unicode"

	"github.com/weaming/alaya/models"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
)

// ScoredEvent 是带相关度分数的事件。
type ScoredEvent struct {
	Event models.Event
	Score float64
}

// Index 是 bigram 倒排索引。
type Index struct {
	postings map[string]map[int]int // token -> 文档下标 -> 词频
	lengths  []int                  // 每篇文档的 token 数
	docs     []models.Event
}

func New() *Index {
	return &Index{postings: make(map[string]map[int]int)}
}

// Build 丢弃现有索引并从给定事件全集重建。
func (idx *Index) Build(events []models.Event) {
	idx.postings = make(map[string]map[int]int)
	idx.lengths = nil
	idx.docs = nil

	for _, event := range events {
		idx.Add(event)
	}
}

func (idx *Index) Add(event models.Event) {
	docIdx := len(idx.docs)
	tokens := tokenize(searchText(event))

	idx.docs = append(idx.docs, event)
	idx.lengths = append(idx.lengths, len(tokens))

	for _, token := range tokens {
		posting, ok := idx.postings[token]
		if !ok {
			posting = make(map[int]int)
			idx.postings[token] = posting
		}
		posting[docIdx]++
	}
}

// Search 按 BM25 打分返回排序结果。
//
// top-k 不是越大越好：外部实测显示超过某个点后召回仍在涨、但噪声会压垮推理，
// 因此上限由调用方显式给出。
func (idx *Index) Search(query string, limit int) []ScoredEvent {
	tokens := tokenize(query)
	if len(tokens) == 0 || len(idx.docs) == 0 {
		return nil
	}

	var (
		total = float64(len(idx.docs))
		avg   = idx.averageLength()
		hits  = make(map[int]float64)
	)

	for _, token := range tokens {
		posting, ok := idx.postings[token]
		if !ok {
			continue
		}

		df := float64(len(posting))
		idf := math.Log(1 + (total-df+0.5)/(df+0.5))

		for docIdx, tf := range posting {
			docLen := float64(idx.lengths[docIdx])
			norm := 1 - bm25B + bm25B*docLen/avg
			hits[docIdx] += idf * (float64(tf) * (bm25K1 + 1)) / (float64(tf) + bm25K1*norm)
		}
	}

	scored := make([]ScoredEvent, 0, len(hits))
	for docIdx, score := range hits {
		scored = append(scored, ScoredEvent{Event: idx.docs[docIdx], Score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].Score != scored[j].Score {
			return scored[i].Score > scored[j].Score
		}

		return models.CompareObserved(scored[i].Event, scored[j].Event) > 0
	})

	if limit > 0 && len(scored) > limit {
		scored = scored[:limit]
	}

	return scored
}

func (idx *Index) averageLength() float64 {
	if len(idx.lengths) == 0 {
		return 1
	}

	total := 0
	for _, length := range idx.lengths {
		total += length
	}

	avg := float64(total) / float64(len(idx.lengths))
	if avg <= 0 {
		return 1
	}

	return avg
}

func searchText(event models.Event) string {
	return strings.Join([]string{event.Subject, event.Predicate, event.Object, event.Note}, " ")
}

// tokenize 把文本切成检索 token：拉丁字母与数字按词，CJK 按 bigram。
func tokenize(text string) []string {
	runes := []rune(strings.ToLower(text))
	tokens := make([]string, 0, len(runes))

	for i := 0; i < len(runes); {
		switch {
		case isCJK(runes[i]):
			start := i
			for i < len(runes) && isCJK(runes[i]) {
				i++
			}

			for k := start; k < i; k++ {
				if k+1 < i {
					tokens = append(tokens, string(runes[k:k+2]))
				} else if k == start {
					tokens = append(tokens, string(runes[k]))
				}
			}

		case isWordRune(runes[i]):
			start := i
			for i < len(runes) && isWordRune(runes[i]) {
				i++
			}
			tokens = append(tokens, string(runes[start:i]))

		default:
			i++
		}
	}

	return tokens
}

func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r) ||
		unicode.Is(unicode.Hangul, r)
}

func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}
