// room.go
package world

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	config "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/world/monster"
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
	// 房内 PvE 怪物（创房时默认刷怪）。
	Monsters *monster.Manager
	// 开荒共享笔记（块级乐观锁）；独立于战斗/聊天。
	Note *RoomNote

	// 场景增量：脏实体；每 fullSyncEvery ticks 做一次 AOI 全量校准。
	// aoiResync：跨格/进房时对该观察者下一 tick 推一次视野全量（避免干等 1s 校准才看见静立玩家）。
	dirtyPlayers  map[string]bool
	dirtyMonsters map[string]bool
	aoiResync     map[string]bool
	tickCount     int
	fullSyncEvery int

	// 房间快照：有变化才写 Redis；tick 里按 snapFlushEvery 合并刷盘（避免 2s 空刷 / 战斗每击写）。
	snapDirty       bool
	snapFlushEvery  int // ticks，默认 ~5s
	lastSnapFlushAt int

	// Kind: "" 普通房；pvp_ladder
	Kind string
	PVP  *PVPMatchState

	// sceneDebugLeft>0 时 BroadcastSceneState 向房内打 SCENEDBG 文本并打日志，每广播减 1。
	sceneDebugLeft int
}

// NewPVPRoom 临时对战房：不刷怪。
func NewPVPRoom(name string, maxPlayers int, server *Server, kind string, pvp *PVPMatchState) *Room {
	room := NewRoom(name, maxPlayers, "", server)
	room.Kind = kind
	room.PVP = pvp
	// 清空默认刷怪（PVP 用人打人）
	room.Monsters = monster.NewManager()
	return room
}

// 创建一个新房间并启动其游戏循环
func NewRoom(name string, maxPlayers int, password string, server *Server) *Room {
	room := &Room{
		Name:            name,
		SessionID:       fmt.Sprintf("%s_%d", name, time.Now().UnixNano()),
		Members:         make(map[string]UserEntity),
		server:          server,
		ticker:          time.NewTicker(33 * time.Millisecond), //30hz
		stopChan:        make(chan bool),
		MaxPlayers:      maxPlayers,
		Password:        password,
		AOIManager:      NewAOIManager(-2000, 2000, -2000, 2000, config.Conf.AOI.GridSize),
		BehaviorManager: NewPlayerBehaviorManager(),
		Monsters:        monster.NewManager(),
		Note:            NewDefaultRaidNote(),
		dirtyPlayers:    make(map[string]bool),
		dirtyMonsters:   make(map[string]bool),
		aoiResync:       make(map[string]bool),
		fullSyncEvery:   30, // 约 1s 全量校准一次
		snapFlushEvery:  150, // 脏快照约 5s 合并刷一次
	}
	room.Monsters.SpawnDefaults(name)
	go room.Run()
	return room
}

// NewRoomFromSnapshot 从 Redis 快照恢复房间（保留 SessionID / 怪血量）。
func NewRoomFromSnapshot(snapName string, maxPlayers int, password, sessionID string, server *Server, monsters []monster.Monster) *Room {
	room := &Room{
		Name:            snapName,
		SessionID:       sessionID,
		Members:         make(map[string]UserEntity),
		server:          server,
		ticker:          time.NewTicker(33 * time.Millisecond),
		stopChan:        make(chan bool),
		MaxPlayers:      maxPlayers,
		Password:        password,
		AOIManager:      NewAOIManager(-2000, 2000, -2000, 2000, config.Conf.AOI.GridSize),
		BehaviorManager: NewPlayerBehaviorManager(),
		Monsters:        monster.NewManager(),
		Note:            NewDefaultRaidNote(),
		dirtyPlayers:    make(map[string]bool),
		dirtyMonsters:   make(map[string]bool),
		aoiResync:       make(map[string]bool),
		fullSyncEvery:   30,
		snapFlushEvery:  150,
	}
	if sessionID == "" {
		room.SessionID = fmt.Sprintf("%s_%d", snapName, time.Now().UnixNano())
	}
	if len(monsters) > 0 {
		room.Monsters.LoadFromSnapshot(monsters)
	} else {
		room.Monsters.SpawnDefaults(snapName)
	}
	go room.Run()
	return room
}

// 房间的核心逻辑循环 (游戏循环)
func (this *Room) Run() {
	fmt.Printf("房间 '%s' 的游戏循环已启动...\n", this.Name)
	for {
		select {
		case <-this.ticker.C:
			if this.BehaviorManager != nil {
				this.BehaviorManager.Tick()
			}
			this.tickCount++
			full := this.fullSyncEvery > 0 && this.tickCount%this.fullSyncEvery == 0
			this.BroadcastSceneState(full)
			this.maybeFlushSnapshot()
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
//   - internal/session/user.go -> HandleBattleCast(...)（MSG_ID_PLAYER_BATTLE op=cast，或旧调试文本 seq|skill|target）
//     -> Room.HandleBattleCast(...) -> PlayerBehaviorManager.ProcessBattleCast(...)
func (this *Room) HandleBattleCast(caster UserEntity, seq int64, skillName, targetName string) string {
	if this.BehaviorManager == nil {
		return "BATTLE_REJECT|behavior_manager_not_ready"
	}
	var target UserEntity
	if targetName != "" {
		target = this.getMemberByName(targetName)
	}
	return this.BehaviorManager.ProcessBattleCast(caster, target, seq, skillName)
}

// HandleBattleCastWithBonus 供 session 调用：玩家互殴或打怪，atkBonus 来自装备。
func (this *Room) HandleBattleCastWithBonus(caster UserEntity, seq int64, skillName, targetName string, atkBonus int64) string {
	if targetName == "" {
		return this.HandleBattleCast(caster, seq, skillName, targetName)
	}
	if this.getMemberByName(targetName) != nil {
		res := this.HandleBattleCast(caster, seq, skillName, targetName)
		this.MarkPlayerDirty(targetName)
		if strings.Contains(res, "downed=1") {
			this.NotifyPlayerDowned(targetName, caster.GetName())
		}
		return res
	}
	if this.Monsters != nil && this.Monsters.Get(targetName) != nil {
		res := this.castOnMonster(caster, seq, skillName, targetName, atkBonus)
		this.MarkMonsterDirty(targetName)
		this.MarkSnapshotDirty()
		return res
	}
	return fmt.Sprintf("BATTLE_MISS|seq=%d|skill=%s|target=%s|reason=not_found", seq, skillName, targetName)
}

// HandleAddBuff 是“房间层 Buff 应用入口”。
// 职责：
// 1) 检查行为管理器是否就绪；
// 2) 把 buff 应用请求交给 PlayerBehaviorManager；
// 3) 统一使用房间 tick（33ms）换算 buff 到期轮次。
//
// 上游调用链：
//   - internal/session/user.go -> HandleAddBuff(...)（MSG_ID_PLAYER_BATTLE op=addbuff）
//     -> Room.HandleAddBuff(...) -> PlayerBehaviorManager.AddBuff(...)
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
//   - internal/session/user.go -> HandleListBuffs(...)（命令 buffs）
//     -> Room.HandleListBuffs(...) -> PlayerBehaviorManager.ListBuffs(...)
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

// MarkPlayerDirty / MarkMonsterDirty 供移动与战斗标记增量。
func (this *Room) MarkPlayerDirty(name string) {
	if name == "" {
		return
	}
	this.roomLock.Lock()
	if this.dirtyPlayers == nil {
		this.dirtyPlayers = make(map[string]bool)
	}
	this.dirtyPlayers[name] = true
	this.roomLock.Unlock()
}

func (this *Room) MarkMonsterDirty(name string) {
	if name == "" {
		return
	}
	this.roomLock.Lock()
	if this.dirtyMonsters == nil {
		this.dirtyMonsters = make(map[string]bool)
	}
	this.dirtyMonsters[name] = true
	this.roomLock.Unlock()
}

// MarkAOIResync 下一 tick 对该玩家推送一次 AOI 视野全量（跨格进入新区、进房等）。
func (this *Room) MarkAOIResync(name string) {
	if name == "" {
		return
	}
	this.roomLock.Lock()
	if this.aoiResync == nil {
		this.aoiResync = make(map[string]bool)
	}
	this.aoiResync[name] = true
	this.roomLock.Unlock()
}

// NotifyAOIGridChanged 实体跨格：给「旧九宫格 + 新九宫格」内观察者都打 aoiResync。
// 否则离开方只 dirty、出视野不再发包，邻居客户端会留下最多约 1s 的幽灵，只能等定时全量。
// 调用方须在 Remove/Add 格子之前取出 oldGID，本方法内部完成换格。
func (this *Room) NotifyAOIGridChanged(mover UserEntity, oldGID, newGID int) {
	if mover == nil || oldGID == newGID {
		return
	}
	oldNear := this.AOIManager.GetSurroundingGridIDs(oldGID)
	oldViewers := this.AOIManager.GetPlayersInGrids(oldNear)

	this.AOIManager.RemovePlayerFromGrid(mover, oldGID)
	this.AOIManager.AddPlayerToGrid(mover, newGID)

	newNear := this.AOIManager.GetSurroundingGridIDs(newGID)
	newViewers := this.AOIManager.GetPlayersInGrids(newNear)

	this.MarkAOIResync(mover.GetName())
	for _, u := range oldViewers {
		this.MarkAOIResync(u.GetName())
	}
	for _, u := range newViewers {
		this.MarkAOIResync(u.GetName())
	}
}

// 广播场景状态：AOI 裁剪；full=false 时只发脏实体（观察者可因 aoiResync 单独全量）。
func (this *Room) BroadcastSceneState(full bool) {
	this.roomLock.Lock()
	members := make([]UserEntity, 0, len(this.Members))
	for _, m := range this.Members {
		members = append(members, m)
	}
	dirtyP := this.dirtyPlayers
	dirtyM := this.dirtyMonsters
	resync := this.aoiResync
	this.dirtyPlayers = make(map[string]bool)
	this.dirtyMonsters = make(map[string]bool)
	this.aoiResync = make(map[string]bool)
	this.roomLock.Unlock()

	allPlayers := make(map[string]protocol.PlayerState, len(members))
	for _, member := range members {
		allPlayers[member.GetName()] = protocol.PlayerState{
			Name: member.GetName(), Position: member.GetPosition(),
		}
	}
	allMobs := monsterStatesForBroadcast(this.Monsters)

	for _, viewer := range members {
		viewerFull := full || resync[viewer.GetName()]
		pos := viewer.GetPosition()
		gid := this.AOIManager.GetGridIDByPos(pos.X, pos.Z)
		nearIDs := this.AOIManager.GetSurroundingGridIDs(gid)
		nearUsers := this.AOIManager.GetPlayersInGrids(nearIDs)
		nearSet := make(map[string]bool, len(nearUsers)+1)
		nearSet[viewer.GetName()] = true
		for _, u := range nearUsers {
			nearSet[u.GetName()] = true
		}

		players := make(map[string]protocol.PlayerState)
		for name, st := range allPlayers {
			if !nearSet[name] {
				continue
			}
			if viewerFull || dirtyP[name] || name == viewer.GetName() {
				players[name] = st
			}
		}
		mobs := make(map[string]protocol.MonsterState)
		for name, st := range allMobs {
			mg := this.AOIManager.GetGridIDByPos(st.Position.X, st.Position.Z)
			inView := false
			for _, id := range nearIDs {
				if id == mg {
					inView = true
					break
				}
			}
			if !inView {
				continue
			}
			if viewerFull || dirtyM[name] {
				mobs[name] = st
			}
		}
		if !viewerFull && len(players) == 0 && len(mobs) == 0 {
			if this.sceneDebugLeft > 0 {
				line := fmt.Sprintf("SCENEDBG|tick=%d|viewer=%s|gid=%d|full=false|resync=%v|skip=1|players=|mobs=0",
					this.tickCount, viewer.GetName(), gid, resync[viewer.GetName()])
				fmt.Println(line)
				viewer.Send(protocol.PackTextMessage(line))
			}
			continue
		}
		payload := protocol.SceneStateBroadcast{Full: viewerFull, Players: players, Monsters: mobs}
		jsonData, _ := json.Marshal(payload)
		msg, _ := json.Marshal(protocol.Message{ID: protocol.MSG_ID_SCENE_STATE, Data: jsonData})
		viewer.Send(msg)

		if this.sceneDebugLeft > 0 {
			pnames := make([]string, 0, len(players))
			for n := range players {
				pnames = append(pnames, n)
			}
			sort.Strings(pnames)
			line := fmt.Sprintf("SCENEDBG|tick=%d|viewer=%s|gid=%d|full=%v|resync=%v|skip=0|players=%s|mobs=%d",
				this.tickCount, viewer.GetName(), gid, viewerFull, resync[viewer.GetName()],
				strings.Join(pnames, ","), len(mobs))
			fmt.Println(line)
			viewer.Send(protocol.PackTextMessage(line))
		}
	}
	if this.sceneDebugLeft > 0 {
		this.sceneDebugLeft--
	}
}

// MarkSnapshotDirty 标记房间快照待写（进房/离房/怪血变化等）。
func (this *Room) MarkSnapshotDirty() {
	if this.PVP != nil || this.Kind == "pvp_ladder" {
		return
	}
	this.snapDirty = true
}

// maybeFlushSnapshot 仅在脏且距上次刷盘达到间隔时写 Redis。
func (this *Room) maybeFlushSnapshot() {
	if !this.snapDirty {
		return
	}
	every := this.snapFlushEvery
	if every <= 0 {
		every = 150
	}
	if this.tickCount-this.lastSnapFlushAt < every {
		return
	}
	this.PersistSnapshot()
}

// PersistSnapshot 房间元数据+怪物写入 Redis（PVP 临时房跳过）。force 路径也走这里（空房销毁等）。
func (this *Room) PersistSnapshot() {
	if this.PVP != nil || this.Kind == "pvp_ladder" {
		this.snapDirty = false
		return
	}
	snap := storage.RoomSnapshot{
		Name: this.Name, SessionID: this.SessionID,
		MaxPlayers: this.MaxPlayers, Password: this.Password, Kind: this.Kind,
	}
	if this.Monsters != nil {
		for _, mo := range this.Monsters.Snapshot() {
			snap.Monsters = append(snap.Monsters, storage.MonsterSnap{
				ID: mo.ID, TemplateID: mo.TemplateID, Name: mo.Name,
				HP: mo.HP, MaxHP: mo.MaxHP, Position: mo.Position,
				ScoreDelta: mo.ScoreDelta, GoldReward: mo.GoldReward,
			})
		}
	}
	storage.SaveRoomSnapshot(snap)
	this.snapDirty = false
	this.lastSnapFlushAt = this.tickCount
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
	this.MarkPlayerDirty(user.GetName())
	this.MarkAOIResync(user.GetName())
	this.MarkSnapshotDirty()

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

	// 如果房间里没人了，就准备销毁（快照仍留 Redis，供进程重启后恢复）
	if len(this.Members) == 0 {
		this.PersistSnapshot() // 空房立即落盘，避免销毁前丢怪状态
		this.Stop()
		fmt.Printf("房间 '%s' 因空无一人已被标记为待销毁（快照已保留）。\n", this.Name)
		return true
	}
	this.MarkSnapshotDirty()
	return false
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
