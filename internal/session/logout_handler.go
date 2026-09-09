package session

import (
	storage "unityserverupgrade/internal/storage"
)

// HandleLogout：logout — 显式登出，保留 TCP 连接可再次 login。
func (u *User) HandleLogout(args string) bool {
	if u.DbKey == "" {
		u.Send(PackTextMessage("当前未登录"))
		return true
	}
	displayName := u.Name
	u.server.MapLock.Lock()
	delete(u.server.OnlineMap, displayName)
	if u.DbKey != "" {
		if cur, ok := u.server.OnlineByUserID[u.DbKey]; ok && cur == u {
			delete(u.server.OnlineByUserID, u.DbKey)
		}
	}
	u.server.MapLock.Unlock()
	storage.DelUserRoute(displayName)
	if u.server.Matchmaker != nil {
		u.server.Matchmaker.CancelAll(displayName, u.DbKey)
	}
	u.resetSessionForAccountSwitch()
	u.Name = u.Addr
	// 登出后仍占连接，重新挂回 OnlineMap（以 Addr 为临时名）
	u.server.MapLock.Lock()
	u.server.OnlineMap[u.Name] = u
	u.server.MapLock.Unlock()
	u.sendTextAndReqDone("LOGOUT_OK", "logout")
	return true
}
