package world

import (
	protocol "unityserverupgrade/internal/protocol"
	"time"
)

// UserEntity 抽象出房间/AOI 需要的“用户能力”，避免 world 与 session 之间出现 import cycle。
type UserEntity interface {
	GetName() string
	GetAddr() string
	GetDbKey() string
	GetPosition() protocol.PlayerPosition
	Send(data []byte)
	ForceTeleport(pos protocol.PlayerPosition)
	SetRoom(r *Room)

	// 以下方法由 server 的长连接主循环调用（避免 world 直接依赖 session 包）
	Online()
	Offline()
	DoMessage(msg string)
	GetLastHeartbeat() time.Time
}

