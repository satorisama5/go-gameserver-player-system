// room.go
package internal

import (
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

type Room struct {
	Name       string
	Members    map[string]*User
	SessionID  string //用于数据库做标识
	server     *Server
	ticker     *time.Ticker // 新增：用于定时广播场景状态的定时器
	stopChan   chan bool    // 新增：用于停止定时器的通道
	MaxPlayers int
	Password   string
	roomLock   sync.RWMutex
	aoiManager *AOIManager
}

// 创建一个新房间并启动其游戏循环
func NewRoom(name string, maxPlayers int, password string, server *Server) *Room {
	room := &Room{
		Name:       name,
		SessionID:  fmt.Sprintf("%s_%d", name, time.Now().UnixNano()),
		Members:    make(map[string]*User),
		server:     server,
		ticker:     time.NewTicker(33 * time.Millisecond), //30hz
		stopChan:   make(chan bool),
		MaxPlayers: maxPlayers,
		Password:   password,
		aoiManager: NewAOIManager(-2000, 2000, -2000, 2000, Conf.AOI.GridSize),
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
	playerStates := make(map[string]PlayerState)
	for _, member := range this.Members {
		playerStates[member.Name] = PlayerState{
			Name:     member.Name,
			Position: member.Position,
		}
	}
	this.roomLock.RUnlock()

	// 2. 构建广播消息
	broadcastData := SceneStateBroadcast{
		Players: playerStates,
	}
	jsonData, _ := json.Marshal(broadcastData)

	// 3. 封装成通用Message格式
	msg := Message{
		ID:   MSG_ID_SCENE_STATE,
		Data: jsonData,
	}
	finalMsgBytes, _ := json.Marshal(msg)

	// 4. 在房间内广播
	for _, member := range this.Members {
		member.Send(finalMsgBytes)
	}
}

// 房间内广播【文本】消息
func (this *Room) BroadcastTextMessage(sender *User, msg string) {
	formattedMsg := fmt.Sprintf("[房间:%s][%s]: %s", this.Name, sender.Name, msg)

	// 【核心修改】我们现在需要把消息广播给房间里的【所有人】，包括发送者自己
	for _, member := range this.Members {
		// 我们不再需要 if member.Name != sender.Name 这个判断了
		member.Send(PackTextMessage(formattedMsg))
	}
}

// 添加成员
func (this *Room) AddMember(user *User) {
	this.roomLock.Lock()
	this.Members[user.Name] = user
	this.roomLock.Unlock()
	user.Room = this

	user.ForceTeleport(user.Position)

	gid := this.aoiManager.GetGridIDByPos(user.Position.X, user.Position.Z)
	this.aoiManager.AddPlayerToGrid(user, gid)

	// 2. 准备广播消息
	joinMsg := fmt.Sprintf("[房间:%s][系统]: %s 加入了房间。", this.Name, user.Name)
	packedMsg := PackTextMessage(joinMsg)

	// 3. 【AOI应用】获取该玩家附近的九宫格玩家
	surroundingGIDs := this.aoiManager.GetSurroundingGridIDs(gid)
	playersToNotify := this.aoiManager.GetPlayersInGrids(surroundingGIDs)

	// 4. 只向这些附近的玩家广播消息
	for _, player := range playersToNotify {
		player.Send(packedMsg)
	}
}

// 移除成员
func (this *Room) RemoveMember(user *User) bool {
	leaveMsg := fmt.Sprintf("[房间:%s][系统]: %s 离开了房间。", this.Name, user.Name)
	packedMsg := PackTextMessage(leaveMsg)

	// 2. 【AOI应用】获取该玩家【最后位置】附近的九宫格玩家
	gid := this.aoiManager.GetGridIDByPos(user.Position.X, user.Position.Z)
	surroundingGIDs := this.aoiManager.GetSurroundingGridIDs(gid)
	playersToNotify := this.aoiManager.GetPlayersInGrids(surroundingGIDs)

	// 3. 只向这些附近的玩家广播消息
	for _, player := range playersToNotify {
		player.Send(packedMsg)
	}

	// 4. 从 AOI 管理器中移除该玩家
	this.aoiManager.RemovePlayerFromGrid(user, gid)

	this.roomLock.Lock()
	delete(this.Members, user.Name)
	this.roomLock.Unlock()
	user.Room = nil

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
