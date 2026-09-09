package session

import (
	"fmt"
	"strings"
	"time"

	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
)

// HandleServers：
//
//	servers          列出 Redis 中存活实例（按 online 升序）
//	servers|best     返回最闲实例（可避开本机若有其它实例）
func (u *User) HandleServers(args string) bool {
	parts := strings.SplitN(args, "|", 2)
	op := ""
	if len(parts) > 0 {
		op = strings.ToLower(strings.TrimSpace(parts[0]))
	}
	list := storage.ListGatewayMetas()
	if op == "best" {
		best, ok := storage.PickLeastLoadedGateway(u.server.InstanceID)
		if !ok {
			u.Send(PackTextMessage("SERVERS_BEST|err=no_instance"))
			return true
		}
		u.Send(PackTextMessage(fmt.Sprintf(
			"SERVERS_BEST|id=%s|tcp=%s|forward=%s|online=%d|max=%d",
			best.InstanceID, best.TCP, best.Forward, best.Online, best.Max)))
		return true
	}
	if len(list) == 0 {
		u.Send(PackTextMessage("SERVERS|n=0|hint=wait_gateway_ttl_refresh"))
		return true
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("SERVERS|n=%d", len(list)))
	for _, m := range list {
		self := 0
		if m.InstanceID == u.server.InstanceID {
			self = 1
		}
		b.WriteString(fmt.Sprintf("|id=%s,tcp=%s,online=%d,max=%d,self=%d",
			m.InstanceID, m.TCP, m.Online, m.Max, self))
	}
	u.Send(PackTextMessage(b.String()))
	return true
}

// HandleMigrate：
//
//	migrate|best              软迁移到最闲其它实例
//	migrate|{instanceId}      软迁移到指定实例
//
// 流程：离房 → 存档 → 清路由 → 下发 MIGRATE → 短延迟 Offline 关连。
func (u *User) HandleMigrate(args string) bool {
	if !u.requireLoggedIn() {
		return true
	}
	target := strings.TrimSpace(args)
	if target == "" {
		u.Send(PackTextMessage("用法: migrate|best 或 migrate|{instanceId}"))
		return true
	}

	var dest storage.GatewayMeta
	var ok bool
	if strings.EqualFold(target, "best") {
		dest, ok = storage.PickLeastLoadedGateway(u.server.InstanceID)
	} else {
		dest, ok = storage.GetGatewayMeta(target)
	}
	if !ok || dest.TCP == "" {
		u.Send(PackTextMessage("MIGRATE_FAIL|reason=target_not_found"))
		return true
	}
	if dest.InstanceID == u.server.InstanceID {
		u.Send(PackTextMessage("MIGRATE_FAIL|reason=already_on_target"))
		return true
	}

	if u.server.Matchmaker != nil {
		u.server.Matchmaker.CancelAll(u.Name, u.DbKey)
	}
	if u.Room != nil {
		roomName := u.Room.Name
		empty := u.Room.RemoveMember(u)
		if empty {
			u.server.RoomLock.Lock()
			delete(u.server.Rooms, roomName)
			u.server.RoomLock.Unlock()
		}
	}

	roomName := ""
	attrs := protocol.DefaultCharacterAttrs()
	if u.Player != nil {
		attrs = u.Player.Attrs
	}
	storage.SavePlayerToDB(u.DbKey, u.Name, u.Position, roomName, attrs)

	displayName := u.Name
	storage.DelUserRoute(displayName)

	u.Send(PackTextMessage(fmt.Sprintf(
		"MIGRATE|tcp=%s|instance=%s|reason=manual|hint=relogin_with_token_or_password",
		dest.TCP, dest.InstanceID)))

	go func() {
		time.Sleep(200 * time.Millisecond)
		u.Offline()
	}()
	return true
}
