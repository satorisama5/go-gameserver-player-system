package internal

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"
)

// CommandHandler 定义了一个指令处理函数的类型签名。
// 它接收指令的参数部分，并返回一个 bool 值表示是否应继续执行后续操作。
type CommandHandler func(args string) bool

type User struct {
	Name                   string
	Addr                   string
	Channel                chan []byte
	conn                   net.Conn
	server                 *Server
	Room                   *Room
	Position               PlayerPosition
	isOnline               bool       // 标记用户是否在线
	mu                     sync.Mutex // 用于保护 isOnline 状态的锁
	DbKey                  string
	lastHeartbeat          time.Time
	lastPositionUpdateTime time.Time // 用于移动速度校验
	packetCount            int       // 用于发包频率统计，计数器
	lastPacketCheckTime    time.Time // 用于发包频率统计.判断是否到达一秒
	expectTeleport         bool      //传送不触发速度检测
	// 【新增】指令处理器映射
	commandHandlers map[string]CommandHandler
}

type RoomInfoDTO struct {
	Name      string `json:"n"`
	CurPlayer int    `json:"c"`
	MaxPlayer int    `json:"m"`
	HasPwd    bool   `json:"p"`
}

func NewUser(conn net.Conn, server *Server) *User {
	userAddr := conn.RemoteAddr().String()

	user := &User{
		Name:                   userAddr,
		Addr:                   userAddr,
		Channel:                make(chan []byte, 256),
		conn:                   conn,
		server:                 server,
		Room:                   nil,
		Position:               Conf.Server.SpawnPoint,
		isOnline:               true,
		DbKey:                  "",
		lastHeartbeat:          time.Now(),
		lastPositionUpdateTime: time.Now(),
		packetCount:            0,
		lastPacketCheckTime:    time.Now(),
	}

	// 【新增】初始化并注册所有带中间件的指令处理器
	user.commandHandlers = make(map[string]CommandHandler)
	user.commandHandlers["rename"] = user.LoggingMiddleware(user.HandleRename)
	user.commandHandlers["who"] = user.LoggingMiddleware(user.HandleListUsers)
	user.commandHandlers["create"] = user.LoggingMiddleware(user.HandleCreateRoom)
	user.commandHandlers["join"] = user.LoggingMiddleware(user.HandleJoinRoom)
	user.commandHandlers["leave"] = user.LoggingMiddleware(user.HandleLeaveRoom)
	user.commandHandlers["rooms"] = user.LoggingMiddleware(user.HandleListRooms)
	user.commandHandlers["chat"] = user.LoggingMiddleware(user.HandleRoomChat)
	user.commandHandlers["pm"] = user.LoggingMiddleware(user.HandlePrivateMessage)
	user.commandHandlers["users"] = user.LoggingMiddleware(user.HandleGetUsersJSON)
	user.commandHandlers["shout"] = user.LoggingMiddleware(user.HandleShout)

	fmt.Printf("为用户 %s 启动 ListenMessage 协程...\n", user.Name)
	go user.ListenMessage()

	return user
}

// LoggingMiddleware 是一个简单的日志中间件。
func (this *User) LoggingMiddleware(next CommandHandler) CommandHandler {
	return func(args string) bool {
		// 1. 在执行真正的业务逻辑前，先打印日志
		fmt.Printf("[中间件日志] 用户 [%s] 正在执行指令, 参数: %s\n", this.Name, args)
		// 2. 调用并返回流水线中的下一个处理器
		return next(args)
	}
}

// 用户上线
func (this *User) Online() {
	req := request{
		reqType: reqTypeRegister,
		user:    this,
	}
	this.server.requests <- req
}

func (this *User) Offline() {
	// 1. 检查是否已经下线 (防止多次调用)
	this.mu.Lock()
	if !this.isOnline {
		this.mu.Unlock()
		return
	}
	this.isOnline = false
	this.mu.Unlock()

	// 2. 数据库保存逻辑 (保持不变)
	if this.DbKey != "" {
		currentRoomName := ""
		if this.Room != nil {
			currentRoomName = this.Room.Name
		}
		fmt.Printf("正在保存用户 %s 的数据到数据库 (Key: %s)...\n", this.Name, this.DbKey)
		// 异步保存，防止阻塞
		go SavePlayerToDB(this.DbKey, this.Name, this.Position, currentRoomName)
	}

	// 3. 房间清理逻辑 (保持不变)
	// 虽然 Map 逻辑移交了，但房间逻辑比较独立，这里先保留处理
	if this.Room != nil {
		roomName := this.Room.Name
		isRoomEmpty := this.Room.RemoveMember(this) // 从房间移除自己
		if isRoomEmpty {
			this.server.roomLock.Lock()
			delete(this.server.Rooms, roomName)
			this.server.roomLock.Unlock()
		}
	}

	req := request{
		reqType: reqTypeUnregister,
		user:    this,
	}
	this.server.requests <- req
	// ------------------

	// 5. 清理连接资源
	this.conn.Close()

	// 关闭自身的 Channel，防止内存泄漏
	// 使用 recover 防止向已关闭的 channel 发送数据导致的 panic
	defer func() {
		if r := recover(); r != nil {
			// 忽略错误
		}
	}()
	close(this.Channel)

	fmt.Printf("用户 %s 已成功下线并发送注销信号。\n", this.Name)
}

func (this *User) HandleShout(args string) bool {
	if args == "" {
		this.Send(PackTextMessage("喊话内容不能为空"))
		return true
	}

	// 格式化消息：[世界]张三: 大家好
	msg := fmt.Sprintf("[世界]%s: %s", this.Name, args)

	// 【关键点】这里调用 Server.BroadCast
	// 这会将一个 reqTypeBroadcast 的请求扔进 channel
	// 最终由 Server.RunLoop 的那个 "闲置 case" 处理
	this.server.BroadCast(this, msg)

	return true
}

// 发送消息 (逻辑不变)
func (this *User) Send(data []byte) {
	defer func() {
		if r := recover(); r != nil {
		}
	}()

	this.mu.Lock()
	if !this.isOnline {
		this.mu.Unlock()
		return
	}
	this.mu.Unlock()

	select {
	case this.Channel <- data:
	default:
		fmt.Printf("用户 %s 的消息通道已满，消息被丢弃。\n", this.Name)
	}
}

// 消息处理入口 (逻辑不变)
func (this *User) DoMessage(msg string) {
	if !this.IsPacketRateValid() {
		// 频率过高，可以选择忽略此包，或者直接踢掉
		// 这里我们选择记录日志并忽略，更温和
		fmt.Printf("警告: 用户 [%s] 发包频率过快，此包被丢弃。\n", this.Name)
		return
	}
	this.lastHeartbeat = time.Now()
	var message Message
	if err := json.Unmarshal([]byte(msg), &message); err != nil {
		fmt.Printf("警告: 用户 %s 发送了非JSON格式的消息: %s\n", this.Name, msg)
		return
	}

	if message.ID == MSG_ID_HEARTBEAT {
		return
	}

	if message.ID != MSG_ID_PLAYER_MOVE_REQ {
		fmt.Printf("收到来自 [%s] 的消息ID: %d\n", this.Name, message.ID)
	}

	switch message.ID {
	case MSG_ID_PLAYER_MOVE_REQ:
		if this.Room != nil {
			var newPos PlayerPosition
			if err := json.Unmarshal(message.Data, &newPos); err == nil {

				// 解析成功后，再进行安全校验
				if this.IsMovementValid(newPos) {
					// --- AOI 位置更新逻辑 ---
					oldGID := this.Room.aoiManager.GetGridIDByPos(this.Position.X, this.Position.Z)
					newGID := this.Room.aoiManager.GetGridIDByPos(newPos.X, newPos.Z)

					if oldGID != newGID {
						this.Room.aoiManager.RemovePlayerFromGrid(this, oldGID)
						this.Room.aoiManager.AddPlayerToGrid(this, newGID)
					}
					// -----------------------

					// 更新服务器权威位置
					this.Position = newPos
					this.lastPositionUpdateTime = time.Now()
				} else {
					// 校验失败（作弊），直接忽略
					return
				}
			}
		}
	case MSG_ID_COMMAND:
		var cmdMsg CommandMessage
		if err := json.Unmarshal(message.Data, &cmdMsg); err == nil {
			this.handleCommand(cmdMsg.Cmd)
		} else {
			fmt.Printf("解析CommandMessage失败: %v\n", err)
		}
	default:
		fmt.Printf("收到未知的消息ID: %d\n", message.ID)
	}
}

// 【重写】handleCommand 方法，使用 map 分发
func (this *User) handleCommand(msg string) {
	fmt.Printf("正在处理来自 [%s] 的指令: %s\n", this.Name, msg)
	parts := strings.SplitN(msg, "|", 2)
	command := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}

	if handler, ok := this.commandHandlers[command]; ok {
		handler(args)
	} else {
		this.Send(PackTextMessage("未知指令: " + command))
	}
}

// 消息打包 (逻辑不变)
func PackTextMessage(msg string) []byte {
	msgData, _ := json.Marshal(msg)
	jsonMsg, _ := json.Marshal(Message{ID: MSG_ID_TEXT_MESSAGE, Data: msgData})
	return jsonMsg
}

// --- 所有指令处理器 (已改造) ---

func (this *User) HandleRename(args string) bool {
	parts := strings.Split(args, "|")
	newName := parts[0]
	uuid := ""

	if len(parts) > 1 {
		uuid = parts[1]
	} else {
		uuid = this.Addr
	}

	if newName == "" {
		this.Send(PackTextMessage("昵称不能为空"))
		return true
	}

	this.server.mapLock.Lock()
	if _, ok := this.server.OnlineMap[newName]; ok {
		this.server.mapLock.Unlock()
		this.Send(PackTextMessage("昵称已被占用"))
		return true
	}
	this.DbKey = fmt.Sprintf("%s_%s", uuid, newName)
	this.server.mapLock.Unlock()

	playerData, exists := LoadPlayerFromDB(this.DbKey)

	this.server.mapLock.Lock()
	if _, ok := this.server.OnlineMap[newName]; ok {
		this.server.mapLock.Unlock()
		this.Send(PackTextMessage("昵称已被占用(并发冲突)"))
		return true
	}

	delete(this.server.OnlineMap, this.Name)
	this.server.OnlineMap[newName] = this
	this.server.mapLock.Unlock()

	oldName := this.Name
	this.Name = newName

	if this.Room != nil {
		delete(this.Room.Members, oldName)
		this.Room.Members[this.Name] = this
	}

	this.Send(PackTextMessage(fmt.Sprintf("RENAME_SUCCESS|%s", this.Name)))

	if exists {
		shouldRestorePosition := false
		if playerData.LastScene != "" {
			this.server.roomLock.RLock()
			_, roomExists := this.server.Rooms[playerData.LastScene]
			this.server.roomLock.RUnlock()

			if roomExists {
				shouldRestorePosition = true
			}
		}
		if shouldRestorePosition {
			// A. 房间还在：恢复旧坐标
			savedPos := PlayerPosition{
				X: playerData.PositionX,
				Y: playerData.PositionY,
				Z: playerData.PositionZ,
			}
			// 这是一个合法的“传送”，给个特赦令
			this.ForceTeleport(savedPos)

			// 发送指令让客户端也加载场景并传送
			saveMsg := fmt.Sprintf("LOAD_SAVE|%s|%.2f,%.2f,%.2f",
				playerData.LastScene, savedPos.X, savedPos.Y, savedPos.Z)
			this.Send(PackTextMessage(saveMsg))

			this.Send(PackTextMessage("欢迎回来 [发现可恢复的房间存档]"))
		} else {
			// B. 房间不在了：必须重置为配置的出生点！
			// 否则 user.Position 会残留旧数据，导致下次开房位置错误
			this.ForceTeleport(Conf.Server.SpawnPoint)

			this.Send(PackTextMessage("欢迎回来 [上次所在的房间已解散，位置已重置]"))
		}

	} else {
		this.ForceTeleport(Conf.Server.SpawnPoint)
		go SavePlayerToDB(this.DbKey, this.Name, this.Position, "")
	}
	return true
}

func (this *User) HandleJoinRoom(args string) bool {
	parts := strings.Split(args, "|")
	roomName := parts[0]
	inputPwd := ""
	if len(parts) > 1 {
		inputPwd = parts[1]
	}
	this.server.roomLock.Lock()

	room, ok := this.server.Rooms[roomName]
	if !ok {
		this.server.roomLock.Unlock()
		this.Send(PackTextMessage("房间不存在"))
		return true
	}

	if len(room.Members) >= room.MaxPlayers {
		this.server.roomLock.Unlock()
		this.Send(PackTextMessage("房间已满"))
		return true
	}

	if room.Password != "" && room.Password != inputPwd {
		this.server.roomLock.Unlock()
		this.Send(PackTextMessage("密码错误"))
		return true
	}

	if this.Room != nil {
		oldRoomName := this.Room.Name
		isOldRoomEmpty := this.Room.RemoveMember(this)
		if isOldRoomEmpty {
			delete(this.server.Rooms, oldRoomName)
		}
	}

	this.server.roomLock.Unlock()

	room.AddMember(this)
	this.Send(PackTextMessage(fmt.Sprintf("成功加入房间 '%s'", roomName)))

	historyLogs := GetRecentChatLogs(room.SessionID, 5)

	if len(historyLogs) > 0 {
		header := fmt.Sprintf("[房间:%s][系统]: -------------- 历史消息 --------------", roomName)
		this.Send(PackTextMessage(header))
		for _, log := range historyLogs {
			msg := fmt.Sprintf("[房间:%s][历史-%s]: %s", roomName, log.Sender, log.Message)
			this.Send(PackTextMessage(msg))
		}
		footer := fmt.Sprintf("[房间:%s][系统]: -------------------------------------", roomName)
		this.Send(PackTextMessage(footer))
	}
	return true
}

func (this *User) HandleListUsers(args string) bool {
	this.server.mapLock.RLock()
	defer this.server.mapLock.RUnlock()
	var userList []string
	for _, user := range this.server.OnlineMap {
		userList = append(userList, user.Name)
	}
	this.Send(PackTextMessage("当前在线用户列表: " + strings.Join(userList, ", ")))
	return true
}

func (this *User) HandleCreateRoom(args string) bool {
	parts := strings.Split(args, "|")
	roomName := parts[0]
	maxPlayers := 4
	password := ""
	if roomName == "" {
		this.Send(PackTextMessage("房间名不能为空"))
		return true
	}
	if len(parts) > 1 {
		fmt.Sscanf(parts[1], "%d", &maxPlayers)
		if maxPlayers < 1 {
			maxPlayers = 1
		}
		if maxPlayers > 20 {
			maxPlayers = 20
		}
	}

	if len(parts) > 2 {
		password = parts[2]
	}
	this.server.roomLock.Lock()
	defer this.server.roomLock.Unlock()
	if _, ok := this.server.Rooms[roomName]; ok {
		this.Send(PackTextMessage("房间已存在"))
		return true
	}
	if this.Room != nil {
		oldRoomName := this.Room.Name
		isOldRoomEmpty := this.Room.RemoveMember(this)
		if isOldRoomEmpty {
			delete(this.server.Rooms, oldRoomName)
		}
	}
	newRoom := NewRoom(roomName, maxPlayers, password, this.server)
	this.server.Rooms[roomName] = newRoom
	newRoom.AddMember(this)
	this.Send(PackTextMessage(fmt.Sprintf("房间 '%s' 创建成功", roomName)))
	return true
}

func (this *User) HandleLeaveRoom(args string) bool {
	if this.Room == nil {
		this.Send(PackTextMessage("你不在任何房间中"))
		return true
	}
	roomName := this.Room.Name
	isRoomEmpty := this.Room.RemoveMember(this)
	if isRoomEmpty {
		this.server.roomLock.Lock()
		delete(this.server.Rooms, roomName)
		this.server.roomLock.Unlock()
	}
	this.Send(PackTextMessage("LEAVE_SUCCESS|你已成功离开房间"))
	return true
}

func (this *User) HandleListRooms(args string) bool {
	this.server.roomLock.RLock()
	defer this.server.roomLock.RUnlock()

	if len(this.server.Rooms) == 0 {
		this.Send(PackTextMessage("当前没有可用房间"))
		return true
	}

	var dtos []RoomInfoDTO
	for _, r := range this.server.Rooms {
		info := RoomInfoDTO{
			Name:      r.Name,
			CurPlayer: len(r.Members),
			MaxPlayer: r.MaxPlayers,
			HasPwd:    r.Password != "",
		}
		dtos = append(dtos, info)
	}

	jsonBytes, _ := json.Marshal(dtos)
	this.Send(PackTextMessage("ROOM_LIST|" + string(jsonBytes)))
	return true
}

func (this *User) HandleRoomChat(args string) bool {
	if this.Room == nil {
		this.Send(PackTextMessage("你不在任何房间中，无法发送房间消息"))
		return true
	}

	safeMsg := WordFilter.Handle(args)
	if len(strings.TrimSpace(safeMsg)) == 0 {
		this.Send(PackTextMessage("不能发送空消息或非法字符"))
		return true
	}
	this.Room.BroadcastTextMessage(this, safeMsg)

	go PublishChatLogToMQ(this.Room.SessionID, this.Name, safeMsg)
	return true
}

func (this *User) HandlePrivateMessage(args string) bool {
	pmParts := strings.SplitN(args, "|", 2)
	if len(pmParts) != 2 {
		this.Send(PackTextMessage("私聊格式错误, 应为: pm|用户名|消息内容"))
		return true
	}

	targetName := pmParts[0]
	msg := pmParts[1]

	this.server.mapLock.RLock()
	targetUser, ok := this.server.OnlineMap[targetName]
	this.server.mapLock.RUnlock()

	if !ok {
		this.Send(PackTextMessage(fmt.Sprintf("用户 '%s' 不在线或不存在", targetName)))
		return true
	}
	if targetUser.Name == this.Name {
		this.Send(PackTextMessage("不能给自己发私聊"))
		return true
	}

	safeMsg := WordFilter.Handle(msg)

	formattedMsg := fmt.Sprintf("[私聊][%s对你说]: %s", this.Name, safeMsg)
	targetUser.Send(PackTextMessage(formattedMsg))

	formattedMsgToSelf := fmt.Sprintf("[私聊][你对%s说]: %s", targetName, safeMsg)
	this.Send(PackTextMessage(formattedMsgToSelf))

	var sessionID string
	if this.Name < targetName {
		sessionID = "PM_" + this.Name + "_" + targetName
	} else {
		sessionID = "PM_" + targetName + "_" + this.Name
	}

	go PublishChatLogToMQ(sessionID, this.Name, safeMsg)

	return true
}

func (this *User) HandleGetUsersJSON(args string) bool {
	this.server.mapLock.RLock()
	defer this.server.mapLock.RUnlock()

	var userList []string
	for name := range this.server.OnlineMap {
		// 也可以在这里直接过滤掉自己，或者交给客户端过滤
		// 为了通用性，我们返回所有人
		userList = append(userList, name)
	}

	// 序列化为 JSON
	jsonBytes, _ := json.Marshal(userList)

	// 发送消息头 USER_LIST|JSON数据
	this.Send(PackTextMessage("USER_LIST|" + string(jsonBytes)))
	return true
}

// 监听消息 (逻辑不变)
func (this *User) ListenMessage() {
	fmt.Printf("用户 %s 的 ListenMessage 协程已成功开始运行。\n", this.Name)
	for data := range this.Channel {
		bytebuf := bytes.NewBuffer([]byte{})
		binary.Write(bytebuf, binary.BigEndian, int16(len(data)))
		binary.Write(bytebuf, binary.BigEndian, data)
		finalData := bytebuf.Bytes()

		if _, err := this.conn.Write(finalData); err != nil {
			break
		}
	}
}

// 【新增方法 - 安全检查 1: 发包频率】
func (this *User) IsPacketRateValid() bool {
	// 检查距离上次检查是否已超过1秒
	if time.Since(this.lastPacketCheckTime) > time.Second {
		// 超过1秒，重置计数器和计时器
		this.packetCount = 0
		this.lastPacketCheckTime = time.Now()
	}

	// 计数器增加
	this.packetCount++

	// 判断是否超过了配置中的阈值
	return this.packetCount <= Conf.Server.MaxPacketsPerSecond
}

// 【新增方法 - 安全检查 2: 移动合法性】
func (this *User) IsMovementValid(newPos PlayerPosition) bool {
	// 【普适性检查】：如果是服务器预期的传送（比如刚进房间、传送门）
	if this.expectTeleport {
		fmt.Printf("用户 [%s] 执行了合法的逻辑传送，跳过速度检测。\n", this.Name)
		this.expectTeleport = false // 消耗掉这张“特赦令”
		return true
	}

	// --- 以下是正常的移动速度检测 ---
	deltaTime := time.Since(this.lastPositionUpdateTime).Seconds()
	if deltaTime <= 0 {
		return false
	}

	distanceSq := (newPos.X-this.Position.X)*(newPos.X-this.Position.X) +
		(newPos.Y-this.Position.Y)*(newPos.Y-this.Position.Y) +
		(newPos.Z-this.Position.Z)*(newPos.Z-this.Position.Z)

	speedSq := float64(distanceSq) / (deltaTime * deltaTime)
	maxSpeedSq := Conf.Server.MaxPlayerSpeed * Conf.Server.MaxPlayerSpeed

	return speedSq <= maxSpeedSq*1.2*1.2
}

func (this *User) ForceTeleport(newPos PlayerPosition) {
	this.mu.Lock()
	defer this.mu.Unlock()

	// 1. 设置标记：告诉安全检查，接下来的位置大跳跃是合法的
	this.expectTeleport = true

	// 2. 更新服务器记录的权威位置
	this.Position = newPos

	// 3. 更新时间戳，防止时间差计算错误
	this.lastPositionUpdateTime = time.Now()

}
