package bedrock

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/weaming/alaya/models"
)

// addrPayload 是参与内容寻址的字段集。
//
// 刻意排除 ObservedAt / RecordedAt / AttributedBy / Note：
// 来源与时间戳的差异不应产生新的内容身份，否则重复写入无法幂等（04 §2.1）。
//
// Supersedes 参与寻址：它声明了与历史的关系，属于断言语义的一部分。
// 若排除它，"先写入 A、再声明 A 取代 B"会因内容相同而被去重，取代关系随之丢失。
type addrPayload struct {
	Version        int                `json:"v"`
	Kind           string             `json:"kind"`
	World          string             `json:"world"`
	Scope          string             `json:"scope"`
	Subject        string             `json:"subject"`
	Predicate      string             `json:"predicate"`
	Object         string             `json:"object"`
	Supersedes     string             `json:"supersedes"`
	ApplicableWhen []models.Condition `json:"applicable_when"`
}

// contentID 计算内容寻址 id：同内容必同 id。
func contentID(event models.Event) (string, error) {
	payload := addrPayload{
		Version:        event.Version,
		Kind:           event.Kind,
		World:          event.World,
		Scope:          event.Scope,
		Subject:        event.Subject,
		Predicate:      event.Predicate,
		Object:         event.Object,
		Supersedes:     event.Supersedes,
		ApplicableWhen: event.ApplicableWhen,
	}

	raw, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("序列化寻址负载: %w", err)
	}

	return hashBytes(raw), nil
}

// chainLink 计算链哈希。篡改任一字节或截断尾部都会破坏该位置及其后的全部 link。
func chainLink(id, prev string, seq int64) string {
	raw := fmt.Sprintf("%s\n%s\n%d", id, prev, seq)
	return hashBytes([]byte(raw))
}

func hashBytes(raw []byte) string {
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}
