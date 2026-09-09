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
	GetATK() int64
	GetDEF() int64
	GetCombatPower() int64
	GetRating() int
	ApplyMatchRatings(newRating int, newLevel int)
	Send(data []byte)
	ForceTeleport(pos protocol.PlayerPosition)
	SetRoom(r *Room)
	MoveToRoom(r *Room) // 离开当前房并加入目标房（PVP 匹配用）
	// BeginSoftMigrate 跨服天梯客机：存档(空房)、清路由、下发 MIGRATE、关连。
	BeginSoftMigrate(hostTCP, hostInstance, reason string)
	// NotifyReplaced 顶号：通知旧连接并异步下线。
	NotifyReplaced(reason string)

	// 以下方法由 server 的长连接主循环调用（避免 world 直接依赖 session 包）
	Online()
	Offline()
	DoMessage(msg string)
	// SubmitInbound 读协程投递上行包：入本连接有序队列，由全局 worker 池执行 DoMessage。
	SubmitInbound(msg string) bool
	GetLastHeartbeat() time.Time
}

