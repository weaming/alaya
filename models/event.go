// Package models 定义基岩事件的形状。
//
// 这是 Alaya 唯一的数据契约：其余各层要么是它的消费者，要么是可由它重建的派生物。
package models

import (
	"fmt"
	"strings"
	"time"
)

// Event 是基岩中的一条不可变事件。写入后不再修改，
// 任何"更新"都是新事件加 Supersedes 指针。
type Event struct {
	Version int    `json:"v"`
	ID      string `json:"id"`
	Link    string `json:"link"`
	Prev    string `json:"prev,omitempty"`
	Seq     int64  `json:"seq"`

	Kind      string `json:"kind"`
	World     string `json:"world"`
	Scope     string `json:"scope"`
	Subject   string `json:"subject"`
	Predicate string `json:"predicate"`
	Object    string `json:"object"`

	ApplicableWhen []Condition `json:"applicable_when,omitempty"`
	Supersedes     string      `json:"supersedes,omitempty"`

	ObservedAt   string `json:"observed_at"`
	RecordedAt   string `json:"recorded_at"`
	AttributedBy string `json:"attributed_by"`
	Note         string `json:"note,omitempty"`
}

// Condition 是声称的适用性条件，对应 03 §2.3 的 applicableWhen。
//
// 当前版本只预留结构、写入恒为空，状态推断（05 §5.4）留待后续实现。
type Condition struct {
	StateVar string `json:"state_var"`
	Op       string `json:"op"`
	Value    string `json:"value"`
}

const (
	// Version 是当前 schema 版本；读取端须能解码所有历史版本。
	Version = 1

	DefaultKind         = "claim"
	DefaultWorld        = "world:real"
	DefaultScope        = "user:garden"
	DefaultAttributedBy = "urn:alaya:agent:assistant"

	// KindDocFact 标记从文档抽取的事实。
	//
	// 它们与手动声称的语义不同：手动声称按 (subject, predicate) 构成一条属性的
	// 时间演化（住址从 A 变 B），而文档里的并列条目是同时成立的多条事实
	// （"有三台相机"不是"相机变过三次"）。混为一谈会让 recall 把并列事实
	// 标注成"已被更新"，误导使用者。
	KindDocFact = "doc_fact"
)

// Triple 标识同一 scope 内的一条断言，用于判定当前值与历史值。
type Triple struct {
	Subject   string
	Predicate string
	World     string
	Scope     string
}

func (e Event) Triple() Triple {
	return Triple{
		Subject:   e.Subject,
		Predicate: e.Predicate,
		World:     e.World,
		Scope:     e.Scope,
	}
}

// EffectiveObservedAt 返回该事件的有效现实时间字符串。
//
// 早期写入未补 ObservedAt 时落盘为空串，直接比较会让"先遍历到的那条"胜出，
// 即次序反转。统一回退到 RecordedAt——它由写入路径保证有值。
//
// 仅供存储与显示使用；**比较先后必须走 CompareObserved**。
func (e Event) EffectiveObservedAt() string {
	if e.ObservedAt != "" {
		return e.ObservedAt
	}

	return e.RecordedAt
}

// CompareObserved 按时刻比较两个事件的有效现实时间：a 晚于 b 返回正数。
//
// 不能直接比较字符串——RFC3339 只在偏移相同时字典序才等于时间序：
// "2026-09-14T09:00:00+08:00" 的字典序大于 "2026-09-14T02:00:00+00:00"，
// 前者实际却早一小时。调用方可传任意偏移，混合偏移是常态而非例外。
//
// 无法解析的畸形输入回退字符串比较，以免被静默判成同一时刻。
func CompareObserved(a, b Event) int {
	left, leftErr := time.Parse(time.RFC3339, a.EffectiveObservedAt())
	right, rightErr := time.Parse(time.RFC3339, b.EffectiveObservedAt())

	if leftErr == nil && rightErr == nil {
		return left.Compare(right)
	}

	return strings.Compare(a.EffectiveObservedAt(), b.EffectiveObservedAt())
}

// Normalize 填充缺省字段。这些默认值都不参与内容寻址，
// 因此补默认值不会改变事件的内容身份。
func (e Event) Normalize() Event {
	if e.Version == 0 {
		e.Version = Version
	}
	if e.Kind == "" {
		e.Kind = DefaultKind
	}
	if e.World == "" {
		e.World = DefaultWorld
	}
	if e.Scope == "" {
		e.Scope = DefaultScope
	}
	if e.AttributedBy == "" {
		e.AttributedBy = DefaultAttributedBy
	}

	return e
}

// Validate 检查事件字段是否可用。
//
// 读取端刻意容忍畸形时间戳（回退字符串比较、不崩溃），
// 但写入端必须拒绝：错误的时间戳会长期参与当前值判定与排序，
// 而调用方得不到任何提示。
func (e Event) Validate() error {
	if e.ObservedAt == "" {
		return nil
	}

	if _, err := time.Parse(time.RFC3339, e.ObservedAt); err != nil {
		return fmt.Errorf("observed_at 不是合法的 RFC3339 时间戳: %q", e.ObservedAt)
	}

	return nil
}
