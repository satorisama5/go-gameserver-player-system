package session

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	grpcnet "google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	config "unityserverupgrade/internal/config"
	mq "unityserverupgrade/internal/mq"
	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
	tool "unityserverupgrade/internal/tool"
	world "unityserverupgrade/internal/world"
	pb "unityserverupgrade/proto"

	"go.mongodb.org/mongo-driver/bson"
)

// CommandHandler 定义了一个指令处理函数的类型签名。
// 它接收指令的参数部分，并返回一个 bool 值表示是否应继续执行后续操作。
type CommandHandler func(args string) bool

func init() {
	// 注入长连接主循环里的用户创建逻辑，避免 world 直接 import session。
	world.SetUserFactory(func(conn net.Conn, server *world.Server) world.UserEntity {
		return NewUser(conn, server)
	})
}

type User struct {
	Name                   string
	Addr                   string
	Channel                chan []byte
	conn                   net.Conn
	server                 *world.Server
	Room                   *world.Room //依赖world
	Position               protocol.PlayerPosition
	Player                 *Player // 人物属性（嵌套）
	isOnline               bool
	mu                     sync.Mutex
	DbKey                  string
	LastHeartbeat          time.Time
	lastPositionUpdateTime time.Time
	packetCount            int
	lastPacketCheckTime    time.Time
	expectTeleport         bool
	currentReqID           string
	commandHandlers        map[string]CommandHandler

	// 上行有序队列：读协程只入队，全局 BusinessPool 串行 drain → DoMessage
	inboundQueue     chan string
	inboundScheduled int32 // 0/1，保证同一连接最多一个 drain 任务在池里
}

type RoomInfoDTO struct {
	Name      string `json:"n"`
	CurPlayer int    `json:"c"`
	MaxPlayer int    `json:"m"`
	HasPwd    bool   `json:"p"`
}

func NewUser(conn net.Conn, server *world.Server) *User {
	userAddr := conn.RemoteAddr().String()
	inboundSize := config.Conf.Server.InboundQueueSize
	if inboundSize <= 0 {
		inboundSize = 64
	}

	user := &User{
		Name:                   userAddr,
		Addr:                   userAddr,
		Channel:                make(chan []byte, 256),
		conn:                   conn,
		server:                 server,
		Room:                   nil,
		Position:               config.Conf.Server.SpawnPoint,
		Player:                 NewDefaultPlayer(),
		isOnline:               true,
		DbKey:                  "",
		LastHeartbeat:          time.Now(),
		lastPositionUpdateTime: time.Now(),
		packetCount:            0,
		lastPacketCheckTime:    time.Now(),
		inboundQueue:           make(chan string, inboundSize),
	}

	// 【新增】初始化并注册所有带中间件的指令处理器
	user.commandHandlers = make(map[string]CommandHandler)
	user.commandHandlers["register"] = user.LoggingMiddleware(user.HandleRegister)
	user.commandHandlers["login"] = user.LoggingMiddleware(user.HandleLogin)
	user.commandHandlers["rename"] = user.LoggingMiddleware(user.HandleSetDisplayName)
	user.commandHandlers["who"] = user.LoggingMiddleware(user.HandleListUsers)
	user.commandHandlers["create"] = user.LoggingMiddleware(user.HandleCreateRoom)
	user.commandHandlers["join"] = user.LoggingMiddleware(user.HandleJoinRoom)
	user.commandHandlers["leave"] = user.LoggingMiddleware(user.HandleLeaveRoom)
	user.commandHandlers["rooms"] = user.LoggingMiddleware(user.HandleListRooms)
	user.commandHandlers["chat"] = user.LoggingMiddleware(user.HandleRoomChat)
	user.commandHandlers["pm"] = user.LoggingMiddleware(user.HandlePrivateMessage)
	user.commandHandlers["addfriend"] = user.LoggingMiddleware(user.HandleAddFriend)
	user.commandHandlers["friends"] = user.LoggingMiddleware(user.HandleListFriends)
	user.commandHandlers["fpm"] = user.LoggingMiddleware(user.HandleFriendPrivateMessage)
	user.commandHandlers["fhistory"] = user.LoggingMiddleware(user.HandleFriendHistory)
	user.commandHandlers["users"] = user.LoggingMiddleware(user.HandleGetUsersJSON)
	user.commandHandlers["shout"] = user.LoggingMiddleware(user.HandleShout)
	user.commandHandlers["shop"] = user.LoggingMiddleware(user.HandleShopList)
	user.commandHandlers["buy"] = user.LoggingMiddleware(user.HandleBuy)
	user.commandHandlers["inventory"] = user.LoggingMiddleware(user.HandleInventory)
	user.commandHandlers["use"] = user.LoggingMiddleware(user.HandleUseItem)
	user.commandHandlers["equip"] = user.LoggingMiddleware(user.HandleEquipItem)
	user.commandHandlers["mobs"] = user.LoggingMiddleware(user.HandleMobList)
	user.commandHandlers["logout"] = user.LoggingMiddleware(user.HandleLogout)
	user.commandHandlers["note"] = user.LoggingMiddleware(user.HandleNoteCommand)
	user.commandHandlers["attrs"] = user.LoggingMiddleware(user.HandleAttrs)
	user.commandHandlers["quest"] = user.LoggingMiddleware(user.HandleQuestCommand)
	user.commandHandlers["pvp"] = user.LoggingMiddleware(user.HandlePVPCommand)
	user.commandHandlers["scenedebug"] = user.LoggingMiddleware(user.HandleSceneDebug)
	user.commandHandlers["warp"] = user.LoggingMiddleware(user.HandleWarp)
	user.commandHandlers["servers"] = user.LoggingMiddleware(user.HandleServers)
	user.commandHandlers["migrate"] = user.LoggingMiddleware(user.HandleMigrate)
	// 战斗与击杀奖励已迁出 command：见 protocol.MSG_ID_PLAYER_BATTLE(2003) / MSG_ID_PLAYER_REWARD(2004)。
	// 开荒笔记正式协议：MSG_ID_NOTE(2005)；command note|... 仅调试。

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
	req := world.Request{
		ReqType: world.ReqTypeRegister,
		User:    this,
	}
	this.server.Requests <- req
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
		attrs := protocol.DefaultCharacterAttrs()
		if this.Player != nil {
			attrs = this.Player.Attrs
		}
		go storage.SavePlayerToDB(this.DbKey, this.Name, this.Position, currentRoomName, attrs)
	}

	// 3. 房间清理逻辑 (保持不变)
	// 虽然 Map 逻辑移交了，但房间逻辑比较独立，这里先保留处理
	if this.Room != nil {
		roomName := this.Room.Name
		isRoomEmpty := this.Room.RemoveMember(this) // 从房间移除自己
		if isRoomEmpty {
			this.server.RoomLock.Lock()
			delete(this.server.Rooms, roomName)
			this.server.RoomLock.Unlock()
		}
	}

	req := world.Request{
		ReqType: world.ReqTypeUnregister,
		User:    this,
	}
	this.server.Requests <- req
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

// SubmitInbound 读协程入口：包进入本连接有序队列，由全局 BusinessPool 串行执行 DoMessage。
// 返回 false 表示队列满或无法调度（调用方应断开连接）。
func (this *User) SubmitInbound(msg string) bool {
	this.mu.Lock()
	online := this.isOnline
	this.mu.Unlock()
	if !online {
		return false
	}
	// 包已到达即刷新活跃时间，避免业务积压时被心跳巡检误踢。
	this.LastHeartbeat = time.Now()

	select {
	case this.inboundQueue <- msg:
	default:
		return false
	}
	this.scheduleInboundDrain()
	return true
}

func (this *User) scheduleInboundDrain() {
	if !atomic.CompareAndSwapInt32(&this.inboundScheduled, 0, 1) {
		return // 已有 drain 在跑或已排队
	}
	pool := this.server.BusinessPool
	if pool == nil {
		atomic.StoreInt32(&this.inboundScheduled, 0)
		this.drainInbound()
		return
	}
	ok := pool.TrySubmit(func() {
		this.drainInbound()
	})
	if !ok {
		atomic.StoreInt32(&this.inboundScheduled, 0)
		// 池满：同步 drain 一小段，避免包永久卡在队列；仍可能拖住读协程，但优于丢状态
		this.drainInbound()
	}
}

// drainInbound 串行消费本连接上行队列；同连接包顺序与读到的顺序一致。
func (this *User) drainInbound() {
	for {
		for {
			select {
			case msg := <-this.inboundQueue:
				this.mu.Lock()
				online := this.isOnline
				this.mu.Unlock()
				if !online {
					atomic.StoreInt32(&this.inboundScheduled, 0)
					return
				}
				this.DoMessage(msg)
			default:
				goto drained
			}
		}
	drained:
		atomic.StoreInt32(&this.inboundScheduled, 0)
		// 清空后若又有入队（与 SubmitInbound 竞态），重新抢一次调度，避免包卡死。
		if len(this.inboundQueue) == 0 {
			return
		}
		if !atomic.CompareAndSwapInt32(&this.inboundScheduled, 0, 1) {
			return
		}
	}
}

// 消息处理入口 (逻辑不变)
func (this *User) DoMessage(msg string) {
	// 1) 基础限速保护
	if !this.IsPacketRateValid() {
		fmt.Printf("警告: 用户 [%s] 发包频率过快，此包被丢弃。\n", this.Name)
		return
	}

	// 2) 解析协议包
	var message protocol.Message
	if err := json.Unmarshal([]byte(msg), &message); err != nil {
		fmt.Printf("警告: 用户 %s 发送了非JSON格式的消息: %s\n", this.Name, msg)
		return
	}

	// 3) 任何合法上行包都刷新连接活跃时间
	this.LastHeartbeat = time.Now()

	// 4) 心跳包只做保活，不进入业务分发
	if message.ID == protocol.MSG_ID_HEARTBEAT {
		return
	}

	isMove := message.ID == protocol.MSG_ID_PLAYER_MOVE_REQ
	// 带 req_id 的角色行为包（命令 / 战斗 / 奖励）共用同一套接入幂等窗口。
	needsReqIDIdem := message.ReqID != "" && (message.ID == protocol.MSG_ID_COMMAND ||
		message.ID == protocol.MSG_ID_PLAYER_BATTLE ||
		message.ID == protocol.MSG_ID_PLAYER_REWARD ||
		message.ID == protocol.MSG_ID_NOTE)

	// 5) 最小幂等预处理：idem:req:{userKey}:{req_id}，见 idemUserKey() 与升级5 文档。
	if needsReqIDIdem {
		idemUserKey := this.idemUserKey()
		if !storage.MarkReqIDIfNew(idemUserKey, message.ReqID, 2*time.Minute) {
			fmt.Printf("幂等拦截: user=%s req_id=%s\n", this.Name, message.ReqID)
			storage.SaveEventLogAsync(storage.EventLog{
				EventType:  "command.idempotent.duplicate",
				Level:      "warn",
				ReqID:      message.ReqID,
				UserKey:    idemUserKey,
				UserName:   this.Name,
				SessionID:  message.SessionID,
				InstanceID: this.server.InstanceID,
				Message:    "duplicate req_id blocked",
			})
			this.Send(PackTextMessageWithReqID("REQ_DUPLICATE|"+message.ReqID, message.ReqID))
			return
		}

		// 显式回显 req_id，客户端可据此做请求-响应关联
		storage.SaveEventLogAsync(storage.EventLog{
			EventType:  "command.idempotent.accept",
			Level:      "info",
			ReqID:      message.ReqID,
			UserKey:    idemUserKey,
			UserName:   this.Name,
			SessionID:  message.SessionID,
			InstanceID: this.server.InstanceID,
			Message:    "req_id accepted",
		})
		this.Send(PackTextMessageWithReqID("REQ_ACCEPT|"+message.ReqID, message.ReqID))
	}

	// 6) 高噪音控制：移动包不打印常规收包日志
	if !isMove {
		fmt.Printf("收到来自 [%s] 的消息ID: %d\n", this.Name, message.ID)
	}

	// 6.5) 除 register/login 外，业务包需先登录
	if message.ID != protocol.MSG_ID_COMMAND && this.DbKey == "" {
		this.Send(PackTextMessage("请先登录：login|账号|密码 或 login|令牌"))
		return
	}

	// 7) 业务分发
	switch message.ID {
	case protocol.MSG_ID_PLAYER_MOVE_REQ:
		if this.Room != nil {
			var newPos protocol.PlayerPosition
			if err := json.Unmarshal(message.Data, &newPos); err == nil {

				// 解析成功后，再进行安全校验
				if this.IsMovementValid(newPos) {
					// --- AOI 位置更新逻辑 ---
					oldGID := this.Room.AOIManager.GetGridIDByPos(this.Position.X, this.Position.Z)
					newGID := this.Room.AOIManager.GetGridIDByPos(newPos.X, newPos.Z)

					if oldGID != newGID {
						// 旧邻居也要 aoiResync，否则 B 飞出后 A 收不到删除，会幽灵到定时全量
						this.Room.NotifyAOIGridChanged(this, oldGID, newGID)
					}
					// -----------------------

					// 更新服务器权威位置
					this.Position = newPos
					this.lastPositionUpdateTime = time.Now()
					this.Room.MarkPlayerDirty(this.Name)
				} else {
					return
				}
			}
		}
	case protocol.MSG_ID_PLAYER_BATTLE:
		this.currentReqID = message.ReqID
		defer func() { this.currentReqID = "" }()
		var p protocol.PlayerBattlePayload
		if err := json.Unmarshal(message.Data, &p); err == nil {
			this.dispatchPlayerBattle(&p)
		} else {
			fmt.Printf("解析 PlayerBattlePayload 失败: %v\n", err)
			this.Send(PackTextMessage("战斗消息 JSON 解析失败"))
		}

	case protocol.MSG_ID_PLAYER_REWARD:
		this.currentReqID = message.ReqID
		defer func() { this.currentReqID = "" }()
		var pr protocol.PlayerKillRewardPayload
		if err := json.Unmarshal(message.Data, &pr); err == nil {
			this.dispatchPlayerKillReward(&pr)
		} else {
			fmt.Printf("解析 PlayerKillRewardPayload 失败: %v\n", err)
			this.Send(PackTextMessage("击杀奖励消息 JSON 解析失败"))
		}

	case protocol.MSG_ID_NOTE:
		this.currentReqID = message.ReqID
		defer func() { this.currentReqID = "" }()
		var np protocol.NotePayload
		if err := json.Unmarshal(message.Data, &np); err == nil {
			this.dispatchRoomNote(&np)
		} else {
			fmt.Printf("解析 NotePayload 失败: %v\n", err)
			this.Send(PackTextMessage("笔记消息 JSON 解析失败"))
		}

	case protocol.MSG_ID_COMMAND:
		var cmdMsg protocol.CommandMessage
		if err := json.Unmarshal(message.Data, &cmdMsg); err == nil {
			this.currentReqID = message.ReqID
			defer func() { this.currentReqID = "" }()
			storage.SaveEventLogAsync(storage.EventLog{
				EventType:  "command.received",
				Level:      "info",
				ReqID:      message.ReqID,
				UserKey:    this.DbKey,
				UserName:   this.Name,
				SessionID:  message.SessionID,
				InstanceID: this.server.InstanceID,
				Message:    "command received",
				Meta: bson.M{
					"command": cmdMsg.Cmd,
				},
			})
			this.handleCommand(cmdMsg.Cmd)
		} else {
			fmt.Printf("解析CommandMessage失败: %v\n", err)
			storage.SaveEventLogAsync(storage.EventLog{
				EventType:  "command.parse.failed",
				Level:      "error",
				ReqID:      message.ReqID,
				UserKey:    this.DbKey,
				UserName:   this.Name,
				SessionID:  message.SessionID,
				InstanceID: this.server.InstanceID,
				Message:    "command payload unmarshal failed",
			})
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

	if !isPublicCommand(command) && this.DbKey == "" {
		this.Send(PackTextMessage("请先登录：login|账号|密码 或 login|令牌"))
		return
	}

	if handler, ok := this.commandHandlers[command]; ok {
		handler(args)
	} else {
		storage.SaveEventLogAsync(storage.EventLog{
			EventType:  "command.unknown",
			Level:      "warn",
			UserKey:    this.DbKey,
			UserName:   this.Name,
			InstanceID: this.server.InstanceID,
			Message:    "unknown command",
			Meta: bson.M{
				"command": command,
				"args":    args,
			},
		})
		this.Send(PackTextMessage("未知指令: " + command))
	}
}

// 消息打包 (逻辑不变)
func PackTextMessage(msg string) []byte {
	msgData, _ := json.Marshal(msg)
	jsonMsg, _ := json.Marshal(protocol.Message{ID: protocol.MSG_ID_TEXT_MESSAGE, Data: msgData})
	return jsonMsg
}

func PackTextMessageWithReqID(msg, reqID string) []byte {
	msgData, _ := json.Marshal(msg)
	jsonMsg, _ := json.Marshal(protocol.Message{
		ID:    protocol.MSG_ID_TEXT_MESSAGE,
		Data:  msgData,
		ReqID: reqID,
	})
	return jsonMsg
}

// --- 所有指令处理器 (已改造)；登录见 auth_handlers.go ---

func (this *User) HandleJoinRoom(args string) bool {
	parts := strings.Split(args, "|")
	roomName := parts[0]
	inputPwd := ""
	if len(parts) > 1 {
		inputPwd = parts[1]
	}
	this.server.RoomLock.Lock()

	room, ok := this.server.Rooms[roomName]
	if !ok {
		this.server.RoomLock.Unlock()
		this.Send(PackTextMessage("房间不存在"))
		return true
	}

	if len(room.Members) >= room.MaxPlayers {
		this.server.RoomLock.Unlock()
		this.Send(PackTextMessage("房间已满"))
		return true
	}

	if room.Password != "" && room.Password != inputPwd {
		this.server.RoomLock.Unlock()
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

	this.server.RoomLock.Unlock()

	room.AddMember(this)
	this.Send(PackTextMessage(fmt.Sprintf("成功加入房间 '%s'", roomName)))

	historyLogs := storage.GetRecentRoomChatLogs(room.SessionID, 5)

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
	this.server.MapLock.RLock()
	defer this.server.MapLock.RUnlock()
	var userList []string
	for _, user := range this.server.OnlineMap {
		userList = append(userList, user.GetName())
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
	this.server.RoomLock.Lock()
	defer this.server.RoomLock.Unlock()
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
	newRoom := world.NewRoom(roomName, maxPlayers, password, this.server)
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
		this.server.RoomLock.Lock()
		delete(this.server.Rooms, roomName)
		this.server.RoomLock.Unlock()
	}
	this.Send(PackTextMessage("LEAVE_SUCCESS|你已成功离开房间"))
	return true
}

func (this *User) HandleListRooms(args string) bool {
	this.server.RoomLock.RLock()
	defer this.server.RoomLock.RUnlock()

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
	if !storage.AllowMessage(this.sessionUserKey()) {
		this.Send(PackTextMessage("发送太频繁"))
		return true
	}
	if this.Room == nil {
		this.Send(PackTextMessage("你不在任何房间中，无法发送房间消息"))
		return true
	}

	safeMsg := tool.WordFilter.Handle(args)
	if len(strings.TrimSpace(safeMsg)) == 0 {
		this.Send(PackTextMessage("不能发送空消息或非法字符"))
		return true
	}
	this.Room.BroadcastTextMessage(this, safeMsg)

	go mq.PublishRoomChatLogToMQ(this.Room.SessionID, this.Name, safeMsg)
	return true
}

func (this *User) HandlePrivateMessage(args string) bool {
	if !storage.AllowMessage(this.sessionUserKey()) {
		this.Send(PackTextMessage("发送太频繁"))
		return true
	}
	pmParts := strings.SplitN(args, "|", 2)
	if len(pmParts) != 2 {
		this.Send(PackTextMessage("私聊格式错误, 应为: pm|用户名|消息内容"))
		return true
	}

	targetName := pmParts[0]
	msg := pmParts[1]

	if targetName == this.Name {
		this.Send(PackTextMessage("不能给自己发私聊"))
		return true
	}

	safeMsg := tool.WordFilter.Handle(msg)
	if err := this.server.DeliverPrivateMessage(targetName, this.Name, safeMsg); err != nil {
		this.Send(PackTextMessage(fmt.Sprintf("用户 '%s' 不在线或不存在", targetName)))
		return true
	}

	formattedMsgToSelf := fmt.Sprintf("[私聊][你对%s说]: %s", targetName, safeMsg)
	this.Send(PackTextMessage(formattedMsgToSelf))

	var conversationID string
	if this.Name < targetName {
		conversationID = "PM_" + this.Name + "_" + targetName
	} else {
		conversationID = "PM_" + targetName + "_" + this.Name
	}

	go mq.PublishPrivateChatLogToMQ(conversationID, this.Name, targetName, safeMsg)

	return true
}

func (this *User) HandleAddFriend(args string) bool {
	friendName := strings.TrimSpace(args)
	if friendName == "" {
		this.Send(PackTextMessage("加好友格式错误, 应为: addfriend|用户名"))
		return true
	}
	if this.DbKey == "" {
		this.Send(PackTextMessage("请先完成改名登录后再加好友"))
		return true
	}
	if friendName == this.Name {
		this.Send(PackTextMessage("不能添加自己为好友"))
		return true
	}

	this.server.MapLock.RLock()
	friendUser, ok := this.server.OnlineMap[friendName]
	this.server.MapLock.RUnlock()
	if !ok || friendUser.GetDbKey() == "" {
		this.Send(PackTextMessage("目标用户不在线或未完成登录"))
		return true
	}

	if err := storage.AddFriend(this.DbKey, this.Name, friendUser.GetDbKey(), friendUser.GetName()); err != nil {
		this.Send(PackTextMessage("添加好友失败，请稍后重试"))
		return true
	}
	if err := storage.AddFriend(friendUser.GetDbKey(), friendUser.GetName(), this.DbKey, this.Name); err != nil {
		this.Send(PackTextMessage("添加好友失败，请稍后重试"))
		return true
	}

	this.Send(PackTextMessage(fmt.Sprintf("ADD_FRIEND_SUCCESS|%s", friendUser.GetName())))
	friendUser.Send(PackTextMessage(fmt.Sprintf("ADD_FRIEND_SUCCESS|%s", this.Name)))
	return true
}

func (this *User) HandleListFriends(args string) bool {
	if this.DbKey == "" {
		this.Send(PackTextMessage("请先完成改名登录后再查看好友"))
		return true
	}
	friendNames := storage.GetFriendNames(this.DbKey)
	if len(friendNames) == 0 {
		this.Send(PackTextMessage("FRIENDS|[]"))
		return true
	}
	jsonBytes, _ := json.Marshal(friendNames)
	this.Send(PackTextMessage("FRIENDS|" + string(jsonBytes)))
	return true
}

func (this *User) HandleFriendPrivateMessage(args string) bool {
	userKey := this.DbKey
	if userKey == "" {
		this.Send(PackTextMessage("请先完成改名登录后再使用好友私聊"))
		return true
	}
	if !storage.AllowMessage(this.sessionUserKey()) {
		this.Send(PackTextMessage("发送太频繁"))
		return true
	}
	pmParts := strings.SplitN(args, "|", 2)
	if len(pmParts) != 2 {
		this.Send(PackTextMessage("好友私聊格式错误, 应为: fpm|用户名|消息内容"))
		return true
	}
	targetName := strings.TrimSpace(pmParts[0])
	msg := pmParts[1]
	if targetName == "" || targetName == this.Name {
		this.Send(PackTextMessage("好友私聊目标不合法"))
		return true
	}
	ok, targetKey := storage.AreFriends(this.DbKey, targetName)
	if !ok || targetKey == "" {
		this.Send(PackTextMessage("对方不是你的好友，请先 addfriend"))
		return true
	}

	safeMsg := tool.WordFilter.Handle(msg)
	conversationID := storage.BuildFriendConversationID(this.DbKey, targetKey)
	seq := storage.NextFriendConversationSeq(conversationID)
	if seq == 0 {
		this.Send(PackTextMessage("好友私聊发送失败：序号分配失败"))
		return true
	}
	if err := storage.SaveFriendChatLog(conversationID, seq, this.Name, targetName, safeMsg); err != nil {
		this.Send(PackTextMessage("好友私聊发送失败：落库失败"))
		return true
	}

	// 先保证落库成功，再尝试实时投递；对方离线时也不影响“消息已发送并可恢复”。
	deliverErr := this.server.DeliverPrivateMessage(targetName, this.Name, safeMsg)
	this.Send(PackTextMessage(fmt.Sprintf("[好友私聊][seq=%d][你对%s说]: %s", seq, targetName, safeMsg)))
	if deliverErr != nil {
		this.Send(PackTextMessage(fmt.Sprintf("[好友私聊][seq=%d] 对方当前不在线，消息已入库，待对方恢复拉取", seq)))
	}
	return true
}

func (this *User) HandleFriendHistory(args string) bool {
	if this.DbKey == "" {
		this.Send(PackTextMessage("请先完成改名登录后再恢复好友消息"))
		return true
	}
	parts := strings.Split(args, "|")
	if len(parts) < 2 {
		this.Send(PackTextMessage("恢复格式错误, 应为: fhistory|好友名|最后seq"))
		return true
	}
	friendName := strings.TrimSpace(parts[0])
	afterSeq := int64(0)
	_, _ = fmt.Sscanf(parts[1], "%d", &afterSeq)

	ok, friendKey := storage.AreFriends(this.DbKey, friendName)
	if !ok || friendKey == "" {
		this.Send(PackTextMessage("该用户不是你的好友"))
		return true
	}
	conversationID := storage.BuildFriendConversationID(this.DbKey, friendKey)
	logs := storage.GetFriendChatLogsSince(conversationID, afterSeq, 50)
	if len(logs) == 0 {
		this.Send(PackTextMessage("FRIEND_HISTORY|[]"))
		return true
	}
	jsonBytes, _ := json.Marshal(logs)
	this.Send(PackTextMessage("FRIEND_HISTORY|" + string(jsonBytes)))
	return true
}

func (this *User) HandleGetUsersJSON(args string) bool {
	this.server.MapLock.RLock()
	defer this.server.MapLock.RUnlock()

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

// dispatchPlayerBattle 处理 MSG_ID_PLAYER_BATTLE（2003），按 op 分发到原战斗处理器。
func (this *User) dispatchPlayerBattle(p *protocol.PlayerBattlePayload) {
	if p == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(p.Op)) {
	case "cast":
		this.HandleBattleCast(fmt.Sprintf("%d|%s|%s", p.Seq, p.Skill, p.Target))
	case "addbuff":
		if p.DurationMs > 0 {
			this.HandleAddBuff(fmt.Sprintf("%s|%d", p.Buff, p.DurationMs))
		} else {
			this.HandleAddBuff(strings.TrimSpace(p.Buff))
		}
	case "buffs":
		this.HandleListBuffs("")
	case "bstate":
		this.HandleBattleState("")
	default:
		this.Send(PackTextMessage("未知战斗 op，支持: cast / addbuff / buffs / bstate"))
	}
}

// dispatchPlayerKillReward 处理 MSG_ID_PLAYER_REWARD（2004）。
func (this *User) dispatchPlayerKillReward(p *protocol.PlayerKillRewardPayload) {
	if p == nil {
		return
	}
	killID := strings.TrimSpace(p.KillID)
	monsterID := strings.TrimSpace(p.MonsterID)
	if killID == "" || monsterID == "" {
		this.Send(PackTextMessage("击杀奖励参数错误: kill_id/monster_id 不能为空"))
		return
	}
	if p.ScoreDelta <= 0 || p.GoldReward <= 0 {
		this.Send(PackTextMessage("击杀奖励参数错误: score_delta / gold_reward 须为正整数"))
		return
	}
	this.executeKillReward(killID, monsterID, p.ScoreDelta, p.GoldReward)
}

// HandleBattleCast 见 combat_handlers.go

// HandleAddBuff: addbuff|buffName|durationMs(可选)
// 说明：buff 生命周期由房间 tick 时间轮推进，不依赖独立定时器。
func (this *User) HandleAddBuff(args string) bool {
	if this.Room == nil {
		this.Send(PackTextMessage("你不在任何房间中，无法添加 Buff"))
		return true
	}
	parts := strings.Split(args, "|")
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		this.Send(PackTextMessage("Buff 格式错误, 应为: addbuff|buffName|durationMs(可选)"))
		return true
	}
	buffName := strings.TrimSpace(parts[0])
	durationMs := int64(3000)
	if len(parts) > 1 && strings.TrimSpace(parts[1]) != "" {
		if _, err := fmt.Sscanf(strings.TrimSpace(parts[1]), "%d", &durationMs); err != nil {
			this.Send(PackTextMessage("Buff 时长格式错误，必须是毫秒整数"))
			return true
		}
	}
	result := this.Room.HandleAddBuff(this, buffName, durationMs)
	this.Send(PackTextMessage(result))
	return true
}

func (this *User) HandleListBuffs(args string) bool {
	if this.Room == nil {
		this.Send(PackTextMessage("你不在任何房间中"))
		return true
	}
	this.Send(PackTextMessage(this.Room.HandleListBuffs(this)))
	return true
}

// HandleBattleState 返回当前玩家战斗链路摘要，方便压测和联调观察。
func (this *User) HandleBattleState(args string) bool {
	if this.Room == nil || this.Room.BehaviorManager == nil {
		this.Send(PackTextMessage("BATTLE_STATE|hp=100|last_seq=0|missing_total=0"))
		return true
	}
	this.Send(PackTextMessage(this.Room.BehaviorManager.GetBattleState(this.GetName())))
	return true
}

// executeKillReward 击杀奖励：调 WalletService.GrantKillReward（内部 kill_reward_outbox + 钱包事务 + Redis 榜分幂等；未完成由后台扫表重试）。
// 幂等：消息层 req_id，与 wallet_ledger / outbox 对齐。
func (this *User) executeKillReward(killID, monsterID string, scoreDelta int32, goldReward int64) {
	if this.DbKey == "" {
		this.Send(PackTextMessage("请先完成改名登录后再提交击杀奖励"))
		return
	}
	reqID := this.currentReqID
	if reqID == "" {
		reqID = "kr_" + killID
	}

	conn, err := grpcnet.NewClient(fmt.Sprintf("127.0.0.1:%d", config.Conf.Server.GrpcPort), grpcnet.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		this.Send(PackTextMessage(fmt.Sprintf("击杀奖励失败(连接钱包服务): %v", err)))
		return
	}
	defer conn.Close()

	walletClient := pb.NewWalletServiceClient(conn)
	result, err := walletClient.GrantKillReward(context.Background(), &pb.GrantKillRewardRequest{
		UserKey:    this.DbKey,
		UserName:   this.Name,
		ReqId:      reqID,
		KillId:     killID,
		MonsterId:  monsterID,
		ScoreDelta: scoreDelta,
		GoldReward: goldReward,
	})
	if err != nil {
		this.Send(PackTextMessage(fmt.Sprintf("击杀奖励失败(钱包服务): %v", err)))
		return
	}

	if result.GetDuplicate() {
		this.Send(PackTextMessage(fmt.Sprintf("KILL_REWARD_DUPLICATE|kill_id=%s|monster=%s|gold=%d|balance=%d|req_id=%s",
			killID, monsterID, goldReward, result.GetBalance(), reqID)))
		return
	}

	this.sendTextAndReqDone(fmt.Sprintf("KILL_REWARD_OK|kill_id=%s|monster=%s|score+%d|gold+%d|balance=%d|req_id=%s",
		killID, monsterID, scoreDelta, goldReward, result.GetBalance(), reqID), "kill_reward")
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
	return this.packetCount <= config.Conf.Server.MaxPacketsPerSecond
}

// 【新增方法 - 安全检查 2: 移动合法性】
func (this *User) IsMovementValid(newPos protocol.PlayerPosition) bool {
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
	maxSpeedSq := config.Conf.Server.MaxPlayerSpeed * config.Conf.Server.MaxPlayerSpeed

	return speedSq <= maxSpeedSq*1.2*1.2
}

func (this *User) ForceTeleport(newPos protocol.PlayerPosition) {
	this.mu.Lock()
	defer this.mu.Unlock()

	this.expectTeleport = true
	oldPos := this.Position
	this.Position = newPos
	this.lastPositionUpdateTime = time.Now()
	if this.Room != nil {
		oldGID := this.Room.AOIManager.GetGridIDByPos(oldPos.X, oldPos.Z)
		newGID := this.Room.AOIManager.GetGridIDByPos(newPos.X, newPos.Z)
		if oldGID != newGID {
			this.Room.NotifyAOIGridChanged(this, oldGID, newGID)
		} else {
			this.Room.MarkAOIResync(this.Name)
		}
		this.Room.MarkPlayerDirty(this.Name)
	}
}

// ---- world.UserEntity 接口适配层（仅用于跨包解耦，不改变业务逻辑）----
func (this *User) GetName() string { return this.Name }

// idemUserKey 短窗 req_id 查重键：已登录用稳定 user_id(DbKey)，未登录用 TCP Addr。
func (this *User) idemUserKey() string {
	if this.DbKey != "" {
		return this.DbKey
	}
	return this.Addr
}

// sessionUserKey 限流/审计等「用户维度」键：优先 DbKey，否则 Addr。
func (this *User) sessionUserKey() string {
	return this.idemUserKey()
}

func (this *User) GetAddr() string { return this.Addr }

func (this *User) GetDbKey() string { return this.DbKey }

func (this *User) GetPosition() protocol.PlayerPosition { return this.Position }

func (this *User) GetATK() int64 {
	if this.Player == nil {
		return protocol.DefaultCharacterAttrs().ATK
	}
	return this.Player.Attrs.ATK
}

func (this *User) GetDEF() int64 {
	if this.Player == nil {
		return protocol.DefaultCharacterAttrs().DEF
	}
	return this.Player.Attrs.DEF
}

func (this *User) GetCombatPower() int64 {
	if this.Player == nil {
		return protocol.DefaultCharacterAttrs().Power()
	}
	return this.Player.Power()
}

func (this *User) GetRating() int {
	if this.Player == nil {
		return world.DefaultEloRating
	}
	if this.Player.Attrs.Rating <= 0 {
		return world.DefaultEloRating
	}
	return this.Player.Attrs.Rating
}

func (this *User) ApplyMatchRatings(newRating, newLevel int) {
	if this.Player == nil {
		this.Player = NewDefaultPlayer()
	}
	this.Player.Attrs.Rating = newRating
	if newLevel > 0 {
		this.Player.Attrs.Level = newLevel
	}
	if this.DbKey != "" {
		go storage.SavePlayerToDB(this.DbKey, this.Name, this.Position, func() string {
			if this.Room != nil {
				return this.Room.Name
			}
			return ""
		}(), this.Player.Attrs)
	}
}

func (this *User) MoveToRoom(r *world.Room) {
	if r == nil {
		return
	}
	if this.Room != nil {
		old := this.Room.Name
		empty := this.Room.RemoveMember(this)
		if empty {
			this.server.RoomLock.Lock()
			delete(this.server.Rooms, old)
			this.server.RoomLock.Unlock()
		}
	}
	r.AddMember(this)
}

// NotifyReplaced 顶号：先通知旧连接，再异步 Offline（关连接、清路由）。
func (this *User) NotifyReplaced(reason string) {
	if reason == "" {
		reason = "KICKED|reason=replaced"
	}
	this.Send(PackTextMessage(reason))
	go func() {
		time.Sleep(80 * time.Millisecond)
		this.Offline()
	}()
}

// BeginSoftMigrate 跨服天梯客机迁移：离房、存档空房、清路由、MIGRATE、Offline。
func (this *User) BeginSoftMigrate(hostTCP, hostInstance, reason string) {
	if hostTCP == "" || hostInstance == "" {
		return
	}
	if this.server.Matchmaker != nil {
		this.server.Matchmaker.Cancel(this.Name)
	}
	storage.CancelCrossPVP(this.DbKey)
	if this.Room != nil {
		roomName := this.Room.Name
		empty := this.Room.RemoveMember(this)
		if empty {
			this.server.RoomLock.Lock()
			delete(this.server.Rooms, roomName)
			this.server.RoomLock.Unlock()
		}
	}
	attrs := protocol.DefaultCharacterAttrs()
	if this.Player != nil {
		attrs = this.Player.Attrs
	}
	if this.DbKey != "" {
		storage.SavePlayerToDB(this.DbKey, this.Name, this.Position, "", attrs)
	}
	if this.Name != "" && this.Name != this.Addr {
		storage.DelUserRoute(this.Name)
	}
	if reason == "" {
		reason = "pvp_cross"
	}
	this.Send(PackTextMessage(fmt.Sprintf(
		"MIGRATE|tcp=%s|instance=%s|reason=%s|hint=relogin_with_token_or_password",
		hostTCP, hostInstance, reason)))
	go func() {
		time.Sleep(200 * time.Millisecond)
		this.Offline()
	}()
}

func (this *User) SetRoom(r *world.Room) { this.Room = r }

func (this *User) GetLastHeartbeat() time.Time { return this.LastHeartbeat }
