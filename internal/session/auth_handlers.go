package session

import (
	"fmt"
	"strings"
	"time"

	"unityserverupgrade/internal/auth"
	config "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
	world "unityserverupgrade/internal/world"
)

// 无需登录即可执行的指令。
func isPublicCommand(command string) bool {
	switch command {
	case "register", "login", "servers":
		return true
	default:
		return false
	}
}

func (this *User) requireLoggedIn() bool {
	if this.DbKey != "" {
		return true
	}
	this.Send(PackTextMessage("请先登录：login|账号|密码 或 login|令牌"))
	return false
}

// HandleRegister：register|账号|密码|游戏昵称
func (this *User) HandleRegister(args string) bool {
	parts := strings.Split(args, "|")
	if len(parts) < 3 {
		this.Send(PackTextMessage("注册格式错误, 应为: register|账号|密码|昵称"))
		return true
	}
	account := parts[0]
	password := parts[1]
	displayName := parts[2]
	if len(parts) > 3 {
		displayName = strings.Join(parts[2:], "|")
	}

	acc, err := storage.CreateAccount(account, password, displayName)
	if err != nil {
		this.Send(PackTextMessage("注册失败: " + err.Error()))
		return true
	}

	token, err := auth.IssueToken(acc.UserID, acc.Account, acc.DisplayName)
	if err != nil {
		this.Send(PackTextMessage("注册成功但签发令牌失败: " + err.Error()))
		return true
	}

	this.finishLogin(acc, token, true)
	return true
}

// HandleLogin：login|账号|密码  或  login|JWT令牌（重连）
func (this *User) HandleLogin(args string) bool {
	args = strings.TrimSpace(args)
	if args == "" {
		this.Send(PackTextMessage("登录格式错误, 应为: login|账号|密码 或 login|令牌"))
		return true
	}

	// 令牌重连：JWT 形如 xxx.yyy.zzz
	if strings.Count(args, ".") >= 2 && !strings.Contains(args, "|") {
		claims, err := auth.ParseToken(args)
		if err != nil {
			this.Send(PackTextMessage("令牌无效或已过期，请重新登录"))
			return true
		}
		acc, err := storage.FindAccountByUserID(claims.UserID)
		if err != nil {
			this.Send(PackTextMessage("登录失败: " + err.Error()))
			return true
		}
		if acc.DisplayName != claims.DisplayName {
			acc.DisplayName = claims.DisplayName
		}
		token, err := auth.IssueToken(acc.UserID, acc.Account, acc.DisplayName)
		if err != nil {
			this.Send(PackTextMessage("刷新令牌失败: " + err.Error()))
			return true
		}
		this.finishLogin(acc, token, false)
		return true
	}

	parts := strings.SplitN(args, "|", 2)
	if len(parts) != 2 {
		this.Send(PackTextMessage("登录格式错误, 应为: login|账号|密码"))
		return true
	}
	acc, err := storage.FindAccountByLogin(parts[0])
	if err != nil {
		this.Send(PackTextMessage(err.Error()))
		return true
	}
	if !storage.CheckPassword(acc.PasswordHash, parts[1]) {
		this.Send(PackTextMessage("账号或密码错误"))
		return true
	}

	token, err := auth.IssueToken(acc.UserID, acc.Account, acc.DisplayName)
	if err != nil {
		this.Send(PackTextMessage("签发令牌失败: " + err.Error()))
		return true
	}
	this.finishLogin(acc, token, false)
	return true
}

// HandleSetDisplayName：rename|新昵称（已登录后改游戏内显示名，非登录）
func (this *User) HandleSetDisplayName(args string) bool {
	if strings.Contains(args, "|") {
		return this.HandleRenameLegacy(args)
	}
	if !this.requireLoggedIn() {
		return true
	}
	newName := strings.TrimSpace(args)
	if newName == "" {
		this.Send(PackTextMessage("昵称不能为空"))
		return true
	}

	if !storage.TryRenameLock(newName) {
		this.Send(PackTextMessage("操作冲突请重试"))
		return true
	}
	defer storage.UnlockRename(newName)

	this.server.MapLock.Lock()
	if existing, ok := this.server.OnlineMap[newName]; ok && existing != this {
		this.server.MapLock.Unlock()
		this.Send(PackTextMessage("昵称已被占用"))
		return true
	}
	this.server.MapLock.Unlock()

	if err := storage.UpdateDisplayName(this.DbKey, newName); err != nil {
		this.Send(PackTextMessage("改名失败: " + err.Error()))
		return true
	}

	this.server.MapLock.Lock()
	if existing, ok := this.server.OnlineMap[newName]; ok && existing != this {
		this.server.MapLock.Unlock()
		this.Send(PackTextMessage("昵称已被占用"))
		return true
	}
	oldName := this.Name
	delete(this.server.OnlineMap, oldName)
	this.Name = newName
	this.server.OnlineMap[newName] = this
	if this.DbKey != "" {
		this.server.OnlineByUserID[this.DbKey] = this
	}
	this.server.MapLock.Unlock()

	if this.Room != nil {
		delete(this.Room.Members, oldName)
		this.Room.Members[this.Name] = this
	}

	this.Send(PackTextMessage(fmt.Sprintf("RENAME_SUCCESS|%s", this.Name)))
	return true
}

// HandleRenameLegacy 兼容旧客户端 rename|昵称|uuid，引导到新登录。
func (this *User) HandleRenameLegacy(args string) bool {
	this.Send(PackTextMessage("rename|昵称|uuid 已废弃，请使用 register|账号|密码|昵称 或 login|账号|密码"))
	return true
}

// resetSessionForAccountSwitch 同一条 TCP 连接登录另一个账号前，清理上一账号的房间/路由状态。
func (this *User) resetSessionForAccountSwitch() {
	if this.Room != nil {
		room := this.Room
		roomName := room.Name
		if room.RemoveMember(this) {
			this.server.RoomLock.Lock()
			delete(this.server.Rooms, roomName)
			this.server.RoomLock.Unlock()
		}
		this.Room = nil
	}
	if this.Name != "" && this.Name != this.Addr {
		storage.DelUserRoute(this.Name)
	}
	if this.DbKey != "" {
		this.server.MapLock.Lock()
		if cur, ok := this.server.OnlineByUserID[this.DbKey]; ok && cur == this {
			delete(this.server.OnlineByUserID, this.DbKey)
		}
		this.server.MapLock.Unlock()
	}
	this.DbKey = ""
}

// finishLogin 在验证通过或注册成功后：顶号踢旧、绑定 DbKey、恢复存档、返回令牌。
func (this *User) finishLogin(acc *storage.AccountModel, token string, isNew bool) {
	if this.server.IsOverloaded() {
		best, ok := storage.PickLeastLoadedGateway(this.server.InstanceID)
		if ok && best.InstanceID != this.server.InstanceID && best.TCP != "" {
			this.Send(PackTextMessage(fmt.Sprintf(
				"LOGIN_REDIRECT|tcp=%s|instance=%s|reason=overloaded|online=%d|max=%d",
				best.TCP, best.InstanceID, this.server.OnlineCount(), this.server.MaxConnections)))
			return
		}
		this.Send(PackTextMessage(fmt.Sprintf(
			"LOGIN_REJECT|reason=overloaded|online=%d|max=%d",
			this.server.OnlineCount(), this.server.MaxConnections)))
		return
	}

	displayName := acc.DisplayName
	userID := acc.UserID

	if this.DbKey != "" && this.DbKey != userID {
		this.resetSessionForAccountSwitch()
	}

	if !storage.TryRenameLock(displayName) {
		this.Send(PackTextMessage("操作冲突请重试"))
		return
	}
	defer storage.UnlockRename(displayName)

	// ---- 顶号：同账号踢旧连接；异账号占昵称则拒绝 ----
	this.server.MapLock.Lock()
	if oldByID, ok := this.server.OnlineByUserID[userID]; ok && oldByID != this {
		this.server.MapLock.Unlock()
		oldByID.NotifyReplaced("KICKED|reason=replaced|msg=账号在别处登录")
		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			this.server.MapLock.RLock()
			_, still := this.server.OnlineByUserID[userID]
			this.server.MapLock.RUnlock()
			if !still {
				break
			}
			time.Sleep(30 * time.Millisecond)
		}
		this.server.MapLock.Lock()
	}
	if existing, ok := this.server.OnlineMap[displayName]; ok && existing != this {
		if existing.GetDbKey() == userID {
			this.server.MapLock.Unlock()
			existing.NotifyReplaced("KICKED|reason=replaced|msg=账号在别处登录")
			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				this.server.MapLock.RLock()
				_, still := this.server.OnlineMap[displayName]
				this.server.MapLock.RUnlock()
				if !still {
					break
				}
				time.Sleep(30 * time.Millisecond)
			}
			this.server.MapLock.Lock()
		} else {
			this.server.MapLock.Unlock()
			this.Send(PackTextMessage("该昵称已在线，请稍后再试或修改昵称"))
			return
		}
	}
	this.server.MapLock.Unlock()

	this.DbKey = userID
	playerData, exists := storage.LoadPlayerFromDB(this.DbKey)

	this.server.MapLock.Lock()
	if existing, ok := this.server.OnlineMap[displayName]; ok && existing != this {
		this.server.MapLock.Unlock()
		this.Send(PackTextMessage("昵称已被占用(并发冲突)"))
		return
	}
	oldName := this.Name
	delete(this.server.OnlineMap, oldName)
	this.Name = displayName
	this.server.OnlineMap[displayName] = this
	this.server.OnlineByUserID[userID] = this
	this.server.MapLock.Unlock()

	if this.Room != nil && oldName != this.Name {
		delete(this.Room.Members, oldName)
		this.Room.Members[this.Name] = this
	}

	this.Send(PackTextMessage(fmt.Sprintf("LOGIN_SUCCESS|%s|%s|%s", token, userID, displayName)))
	this.Send(PackTextMessage(fmt.Sprintf("RENAME_SUCCESS|%s", displayName)))

	routeTTL := time.Duration(config.Conf.Server.RouteTTLSeconds) * time.Second
	if routeTTL <= 0 {
		routeTTL = 30 * time.Second
	}
	storage.SetUserRoute(displayName, this.server.InstanceID, routeTTL)

	if this.Player == nil {
		this.Player = NewDefaultPlayer()
	}

	if exists {
		this.applyPlayerSave(playerData)
		if storage.IsUserIdentityMatched(this.DbKey, this.Name) {
			this.Send(PackTextMessage("FRIEND_RECOVER_READY|身份校验通过，可使用 fhistory|好友名|最后seq 恢复好友私聊"))
		}
	} else {
		this.ForceTeleport(config.Conf.Server.SpawnPoint)
		if isNew {
			go storage.SavePlayerToDB(this.DbKey, this.Name, this.Position, "", this.Player.Attrs)
		}
	}
	this.Send(PackTextMessage(fmt.Sprintf("ATTRS|level=%d|hp=%d/%d|mp=%d/%d|atk=%d|def=%d|power=%d|rating=%d",
		this.Player.Attrs.Level, this.Player.Attrs.HP, this.Player.Attrs.MaxHP,
		this.Player.Attrs.MP, this.Player.Attrs.MaxMP,
		this.Player.Attrs.ATK, this.Player.Attrs.DEF, this.Player.Power(), this.GetRating())))
	this.tryJoinCrossPVPPending()
}

func (this *User) applyPlayerSave(playerData *storage.PlayerModel) {
	if this.Player == nil {
		this.Player = NewDefaultPlayer()
	}
	if playerData.Attrs.Level > 0 || playerData.Attrs.MaxHP > 0 || playerData.Attrs.Rating > 0 {
		this.Player.Attrs = playerData.Attrs
		if this.Player.Attrs.Rating <= 0 {
			this.Player.Attrs.Rating = world.DefaultEloRating
		}
	} else {
		this.Player.Attrs = protocol.DefaultCharacterAttrs()
	}

	shouldRestorePosition := false
	roomName := playerData.LastScene
	if roomName != "" {
		this.server.RoomLock.RLock()
		_, roomExists := this.server.Rooms[roomName]
		this.server.RoomLock.RUnlock()
		if !roomExists {
			// 进程重启后：尝试从 Redis 恢复房间壳
			if restored := this.server.TryRestoreRoom(roomName); restored != nil {
				roomExists = true
			}
		}
		if roomExists {
			shouldRestorePosition = true
		}
	}
	if shouldRestorePosition {
		savedPos := protocol.PlayerPosition{
			X: playerData.PositionX,
			Y: playerData.PositionY,
			Z: playerData.PositionZ,
		}
		this.ForceTeleport(savedPos)
		// 自动重新加入房间
		this.server.RoomLock.RLock()
		room := this.server.Rooms[roomName]
		this.server.RoomLock.RUnlock()
		if room != nil && this.Room == nil {
			if len(room.Members) < room.MaxPlayers {
				room.AddMember(this)
			}
		}
		saveMsg := fmt.Sprintf("LOAD_SAVE|%s|%.2f,%.2f,%.2f",
			roomName, savedPos.X, savedPos.Y, savedPos.Z)
		this.Send(PackTextMessage(saveMsg))
		this.Send(PackTextMessage("欢迎回来 [房间已从快照恢复/仍在线]"))
	} else {
		this.ForceTeleport(config.Conf.Server.SpawnPoint)
		this.Send(PackTextMessage("欢迎回来 [上次所在的房间无法恢复，位置已重置]"))
	}
}

// HandleAttrs 查看自身属性。
func (this *User) HandleAttrs(args string) bool {
	if !this.requireLoggedIn() {
		return true
	}
	if this.Player == nil {
		this.Player = NewDefaultPlayer()
	}
	a := this.Player.Attrs
	this.Send(PackTextMessage(fmt.Sprintf("ATTRS|level=%d|hp=%d/%d|mp=%d/%d|atk=%d|def=%d|crit=%d|power=%d|rating=%d",
		a.Level, a.HP, a.MaxHP, a.MP, a.MaxMP, a.ATK, a.DEF, a.CritRate, this.Player.Power(), this.GetRating())))
	return true
}
