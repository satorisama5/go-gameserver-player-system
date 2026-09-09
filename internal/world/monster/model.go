package monster

import protocol "unityserverupgrade/internal/protocol"

// Monster 房间内的 PvE 目标（非玩家实体）。
type Monster struct {
	ID         string
	TemplateID string
	Name       string
	HP         int64
	MaxHP      int64
	Position   protocol.PlayerPosition
	ScoreDelta int32
	GoldReward int64
}
