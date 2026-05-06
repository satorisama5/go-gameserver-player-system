// room.go
package world

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
	config "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
)

type Room struct {
	Name       string
	Members    map[string]UserEntity
	SessionID  string //用于数据库做标识
	server     *Server
	ticker     *time.Ticker // 新增：用于定时广播场景状态的定时器
	stopChan   chan bool    // 新增：用于停止定时器的通道
	MaxPlayers int
	Password   string
	roomLock   sync.RWMutex
	AOIManager *AOIManager
	// 房间级玩家行为管理器：战斗动作链、Buff 时间轮等都在这里统一调度。
	BehaviorManager *PlayerBehaviorManager
}

// 创建一个新房间并启动其游戏循环
func NewRoom(name string, maxPlayers int, password string, server *Server) *Room {
	room := &Room{
		Name:       name,
		SessionID:  fmt.Sprintf("%s_%d", name, time.Now().UnixNano()),
		Members:    make(map[string]UserEntity),
		server:     server,
		ticker:     time.NewTicker(33 * time.Millisecond), //30hz
		stopChan:   make(chan bool),
		MaxPlayers: maxPlayers,
		Password:   password,
		AOIManager: NewAOIManager(-2000, 2000, -2000, 2000, config.Conf.AOI.GridSize),
		BehaviorManager: NewPlayerBehaviorManager(),
	}
	go room.Run() // 【重要】在创建时就启动房间的逻辑循环
	return room
}

// 房间的核心逻辑循环 (游戏循环)
func (this *Room) Run() {
	fmt.Printf("房间 '%s' 的游戏循环已启动...\n", this.Name)
	for {
		select {
		case <-this.ticker.C:
			if this.BehaviorManager != nil {
				// 行为系统与房间状态广播共用同一 Tick，避免多套时钟漂移。
				this.BehaviorManager.Tick()
			}
			// 定时器触发，广播当前场景的所有玩家状态
			this.BroadcastSceneState()
		case <-this.stopChan:
			// 收到停止信号，停止定时器并退出循环
			this.ticker.Stop()
			fmt.Printf("房间 '%s' 的游戏循环已停止。\n", this.Name)
			return
		}
	}
}

// getMemberByName 在房间成员表中按名字查找目标玩家。
// 作用：
// - 供战斗链路按 targetName 获取目标实体。
// 并发说明：
// - 使用 roomLock 读锁，避免与 AddMember/RemoveMember 写冲突。
// 调用方：
// - HandleBattleCast(...)
func (this *Room) getMemberByName(name string) UserEntity {
	this.roomLock.RLock()
	defer this.roomLock.RUnlock()
	return this.Members[name]
}

// HandleBattleCast 是“房间层战斗入口”。
// 职责：
// 1) 检查行为管理器是否就绪；
// 2) 按目标名字在当前房间查找目标玩家；
// 3) 把战斗动作转交给 PlayerBehaviorManager 做序号/冷却/命中/伤害处理。
//
// 为什么放在 Room：
// - Room 负责“成员范围”，可确保战斗目标必须在同一房间内。
//
// 上游调用链：
// - internal/session/user.go -> HandleBattleCast(...)（MSG_ID_PLAYER_BATTLE op=cast，或旧调试文本 seq|skill|target）
//   -> Room.HandleBattleCast(...) -> PlayerBehaviorManager.ProcessBattleCast(...)
func (this *Room) HandleBattleCast(caster UserEntity, seq int64, skillName, targetName string) string {
	if this.BehaviorManager == nil {
		return "BATTLE_REJECT|behavior_manager_not_ready"
	}
	var target UserEntity
	if targetName != "" {
		// 这里按房间成员查目标，意味着战斗目标必须在同房间内。
		target = this.getMemberByName(targetName)
	}
	return this.BehaviorManager.ProcessBattleCast(caster, target, seq, skillName)
}

// HandleAddBuff 是“房间层 Buff 应用入口”。
// 职责：
// 1) 检查行为管理器是否就绪；
// 2) 把 buff 应用请求交给 PlayerBehaviorManager；
// 3) 统一使用房间 tick（33ms）换算 buff 到期轮次。
//
// 上游调用链：
// - internal/session/user.go -> HandleAddBuff(...)（MSG_ID_PLAYER_BATTLE op=addbuff）
//   -> Room.HandleAddBuff(...) -> PlayerBehaviorManager.AddBuff(...)
func (this *Room) HandleAddBuff(user UserEntity, buffName string, durationMs int64) string {
	if this.BehaviorManager == nil {
		return "BUFF_APPLY_FAILED|behavior_manager_not_ready"
	}
	// room tick 固定 33ms（约 30Hz）。
	return this.BehaviorManager.AddBuff(user.GetName(), buffName, durationMs, 33)
}

// HandleListBuffs 返回某个玩家当前激活 Buff 列表（JSON 字符串）。
// 职责：
// 1) 从行为管理器读取玩家 buff 名称列表；
// 2) 转成现有文本协议格式：BUFF_LIST|[...]
//
// 上游调用链：
// - internal/session/user.go -> HandleListBuffs(...)（命令 buffs）
//   -> Room.HandleListBuffs(...) -> PlayerBehaviorManager.ListBuffs(...)
func (this *Room) HandleListBuffs(user UserEntity) string {
	if this.BehaviorManager == nil {
		return "BUFF_LIST|[]"
	}
	buffs := this.BehaviorManager.ListBuffs(user.GetName())
	if len(buffs) == 0 {
		return "BUFF_LIST|[]"
	}
	jsonBytes, _ := json.Marshal(buffs)
	return "BUFF_LIST|" + string(jsonBytes)
}

// 停止房间的循环
func (this *Room) Stop() {
	// 使用 select 防止重复关闭 channel 导致 panic
	select {
	case this.stopChan <- true:
	default:
	}
}

// 广播场景状态
func (this *Room) BroadcastSceneState() {
	// 【注意】这里需要同时锁住 server.mapLock 和 room 自身的成员列表
	// 但为了简化，我们假设在房间内的玩家状态不会在广播期间被外部修改
	// 更严谨的做法是为 Room 的 Members 也增加一个锁
	this.roomLock.RLock()
	// 1. 构建场景中所有玩家的状态
	playerStates := make(map[string]protocol.PlayerState)
	for _, member := range this.Members {
		playerStates[member.GetName()] = protocol.PlayerState{
			Name:     member.GetName(),
			Position: member.GetPosition(),
		}
	}
	this.roomLock.RUnlock()

	// 2. 构建广播消息
	broadcastData := protocol.SceneStateBroadcast{
		Players: playerStates,
	}
	jsonData, _ := json.Marshal(broadcastData)

	// 3. 封装成通用Message格式
	msg := protocol.Message{
		ID:   protocol.MSG_ID_SCENE_STATE,
		Data: jsonData,
	}
	finalMsgBytes, _ := json.Marshal(msg)

	// 4. 在房间内广播
	for _, member := range this.Members {
		member.Send(finalMsgBytes)
	}
}

// 房间内广播【文本】消息
func (this *Room) BroadcastTextMessage(sender UserEntity, msg string) {
	formattedMsg := fmt.Sprintf("[房间:%s][%s]: %s", this.Name, sender.GetName(), msg)

	// 【核心修改】我们现在需要把消息广播给房间里的【所有人】，包括发送者自己
	for _, member := range this.Members {
		// 我们不再需要 if member.Name != sender.Name 这个判断了
		member.Send(protocol.PackTextMessage(formattedMsg))
	}
}

// 添加成员
func (this *Room) AddMember(user UserEntity) {
	this.roomLock.Lock()
	this.Members[user.GetName()] = user
	this.roomLock.Unlock()
	user.SetRoom(this)
	user.ForceTeleport(user.GetPosition())

	pos := user.GetPosition()
	gid := this.AOIManager.GetGridIDByPos(pos.X, pos.Z)
	this.AOIManager.AddPlayerToGrid(user, gid)

	// 2. 准备广播消息
	joinMsg := fmt.Sprintf("[房间:%s][系统]: %s 加入了房间。", this.Name, user.GetName())
	packedMsg := protocol.PackTextMessage(joinMsg)

	// 3. 【AOI应用】获取该玩家附近的九宫格玩家
	surroundingGIDs := this.AOIManager.GetSurroundingGridIDs(gid)
	playersToNotify := this.AOIManager.GetPlayersInGrids(surroundingGIDs)

	// 4. 只向这些附近的玩家广播消息
	for _, player := range playersToNotify {
		player.Send(packedMsg)
	}
}

// 移除成员
func (this *Room) RemoveMember(user UserEntity) bool {
	leaveMsg := fmt.Sprintf("[房间:%s][系统]: %s 离开了房间。", this.Name, user.GetName())
	packedMsg := protocol.PackTextMessage(leaveMsg)

	// 2. 【AOI应用】获取该玩家【最后位置】附近的九宫格玩家
	pos := user.GetPosition()
	gid := this.AOIManager.GetGridIDByPos(pos.X, pos.Z)
	surroundingGIDs := this.AOIManager.GetSurroundingGridIDs(gid)
	playersToNotify := this.AOIManager.GetPlayersInGrids(surroundingGIDs)

	// 3. 只向这些附近的玩家广播消息
	for _, player := range playersToNotify {
		player.Send(packedMsg)
	}

	// 4. 从 AOI 管理器中移除该玩家
	this.AOIManager.RemovePlayerFromGrid(user, gid)

	this.roomLock.Lock()
	delete(this.Members, user.GetName())
	this.roomLock.Unlock()
	user.SetRoom(nil)

	// 如果房间里没人了，就准备销毁
	if len(this.Members) == 0 {
		this.Stop() // 停止游戏循环
		fmt.Printf("房间 '%s' 因空无一人已被标记为待销毁。\n", this.Name)
		return true // 【修改】返回 true
	}

	return false // 【修改】返回 false
}

//func (this *Room) IsFull() bool {
//	this.roomLock.RLock()
//	defer this.roomLock.RUnlock()
//	return len(this.Members) >= this.MaxPlayers
//}
//
//func (this *Room) CheckPassword(inputPwd string) bool {
//	// 如果没密码，直接通过
//	if this.Password == "" {
//		return true
//	}
//	return this.Password == inputPwd
//}
