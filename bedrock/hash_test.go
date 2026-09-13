package bedrock

import (
	"testing"

	"github.com/weaming/alaya/models"
)

func baseEvent() models.Event {
	return models.Event{
		Version:   models.Version,
		Kind:      models.DefaultKind,
		World:     models.DefaultWorld,
		Scope:     models.DefaultScope,
		Subject:   "urn:alaya:person:user:garden",
		Predicate: "prefers",
		Object:    "hand_drip",
	}
}

func TestContentIDIsDeterministic(t *testing.T) {
	first, err := contentID(baseEvent())
	if err != nil {
		t.Fatalf("计算内容哈希: %v", err)
	}

	second, err := contentID(baseEvent())
	if err != nil {
		t.Fatalf("计算内容哈希: %v", err)
	}

	if first != second {
		t.Fatalf("同一内容得到不同哈希: %s != %s", first, second)
	}
}

// 非寻址字段的变化不得改变内容身份，否则重复写入无法幂等。
func TestContentIDIgnoresNonAddressFields(t *testing.T) {
	base, err := contentID(baseEvent())
	if err != nil {
		t.Fatalf("计算内容哈希: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*models.Event)
	}{
		{"observed_at", func(e *models.Event) { e.ObservedAt = "2020-01-01T00:00:00+08:00" }},
		{"recorded_at", func(e *models.Event) { e.RecordedAt = "2020-01-01T00:00:00+08:00" }},
		{"attributed_by", func(e *models.Event) { e.AttributedBy = "urn:alaya:person:someone" }},
		{"note", func(e *models.Event) { e.Note = "补充说明" }},
		{"prev", func(e *models.Event) { e.Prev = "sha256:whatever" }},
		{"seq", func(e *models.Event) { e.Seq = 99 }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := baseEvent()
			test.mutate(&event)

			got, err := contentID(event)
			if err != nil {
				t.Fatalf("计算内容哈希: %v", err)
			}
			if got != base {
				t.Fatalf("%s 不应参与内容寻址，却改变了哈希", test.name)
			}
		})
	}
}

// 寻址字段任一变化都必须产生新的内容身份。
func TestContentIDTracksAddressFields(t *testing.T) {
	base, err := contentID(baseEvent())
	if err != nil {
		t.Fatalf("计算内容哈希: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(*models.Event)
	}{
		{"object", func(e *models.Event) { e.Object = "espresso" }},
		{"predicate", func(e *models.Event) { e.Predicate = "dislikes" }},
		{"subject", func(e *models.Event) { e.Subject = "urn:alaya:person:other" }},
		{"scope", func(e *models.Event) { e.Scope = "user:other" }},
		{"world", func(e *models.Event) { e.World = "world:fate" }},
		{"supersedes", func(e *models.Event) { e.Supersedes = "sha256:old" }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			event := baseEvent()
			test.mutate(&event)

			got, err := contentID(event)
			if err != nil {
				t.Fatalf("计算内容哈希: %v", err)
			}
			if got == base {
				t.Fatalf("%s 参与内容寻址，变化后哈希却未变", test.name)
			}
		})
	}
}

func TestChainLinkDependsOnPosition(t *testing.T) {
	id := "sha256:abc"

	first := chainLink(id, "", 1)
	second := chainLink(id, first, 2)

	if first == second {
		t.Fatal("不同链位置应得到不同链哈希")
	}

	if again := chainLink(id, "", 1); again != first {
		t.Fatal("同一位置重复计算的链哈希不稳定")
	}

	if other := chainLink(id, "sha256:other", 2); other == second {
		t.Fatal("前序哈希未参与链哈希计算")
	}
}
