package mcpserver

import "github.com/weaming/alaya/models"

// newEvent 把工具参数转成待写入的事件；缺省字段由 models 层补齐。
func newEvent(args claimArgs) models.Event {
	return models.Event{
		Kind:       models.DefaultKind,
		World:      args.World,
		Scope:      args.Scope,
		Subject:    args.Subject,
		Predicate:  args.Predicate,
		Object:     args.Object,
		Supersedes: args.Supersedes,
		ObservedAt: args.ObservedAt,
		Note:       args.Note,
	}
}
