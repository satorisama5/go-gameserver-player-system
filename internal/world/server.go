package world

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	cfg "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
)

const (
	ReqTypeRegister = iota
	ReqTypeUnregister
	ReqTypeBroadcast
)
const (
	HeartbeatTimeout = 30 * time.Second
)

type Request struct {
	ReqType int
	User    UserEntity
	Msg     []byte
}

type Server struct {
	Ip   string
	Port int
	InstanceID string
	routeTTL   time.Duration
	ForwardListenAddr string
	ForwardPublicAddr string

	// 【混合模式】保留 mapLock，主要用于保护读操作
	OnlineMap map[string]UserEntity
	MapLock   sync.RWMutex

	Rooms    map[string]*Room
	RoomLock sync.RWMutex

	Requests chan Request
}

type UserFactoryFunc func(conn net.Conn, server *Server) UserEntity

var userFactory UserFactoryFunc

// SetUserFactory 用于在不产生 import cycle 的情况下，由 session 包注入 User 创建逻辑。
func SetUserFactory(f UserFactoryFunc) {
	userFactory = f
}

// requests 通道缓冲需 >= max_connections，否则大量同时建连时，第 (缓冲+1) 个连接会阻塞在 Online()，无法读包，客户端 Write 也会卡住，表现为“压测只能到约 250 连接”。
func NewServer(ip string, port int) *Server {
	buf := cfg.Conf.Server.MaxConnections * 2
	if buf < 2048 {
		buf = 2048
	}
	instanceID := cfg.Conf.Server.InstanceID
	if instanceID == "" {		instanceID = fmt.Sprintf("%s:%d", ip, port)
	}
	routeTTL := time.Duration(cfg.Conf.Server.RouteTTLSeconds) * time.Second
	if routeTTL <= 0 {
		routeTTL = 30 * time.Second
	}
	forwardListenAddr := cfg.Conf.Server.ForwardListenAddr
	if forwardListenAddr == "" {
		forwardListenAddr = ":18080"
	}
	forwardPublicAddr := cfg.Conf.Server.ForwardPublicAddr
	if forwardPublicAddr == "" {
		forwardPublicAddr = "127.0.0.1" + forwardListenAddr
	}
	return &Server{
		Ip:        ip,
		Port:      port,
		InstanceID: instanceID,
		routeTTL:   routeTTL,
		ForwardListenAddr: forwardListenAddr,
		ForwardPublicAddr: forwardPublicAddr,
		OnlineMap: make(map[string]UserEntity),
		Rooms:     make(map[string]*Room),
		Requests:  make(chan Request, buf),
	}
}

func (this *Server) Start() {
	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", this.Ip, this.Port))
	if err != nil {
		fmt.Println("net.Listen err:", err)
		return
	}
	defer listener.Close()

	go this.RunLoop()

	go this.CleanupDeadConnections()
	go this.RefreshRoutesLoop()
	go this.RefreshGatewayLoop()
	go this.StartForwardServer()

	for {
		conn, err := listener.Accept() //关键代码
		if err != nil {
			fmt.Println("listener accept err:", err)
			continue
		}
		this.MapLock.RLock()
		currentConnections := len(this.OnlineMap)
		this.MapLock.RUnlock()

		if currentConnections >= cfg.Conf.Server.MaxConnections {
			log.Printf("连接被拒绝: 服务器已满 (%d/%d)", currentConnections, cfg.Conf.Server.MaxConnections)
			// 【重要】拒绝后要立刻关闭连接，并 continue 到下一次 accept
			conn.Close()
			continue
		}
		go this.Handler(conn)
	}
}

func (this *Server) CleanupDeadConnections() {
	// 每 10 秒检查一次
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// 用来存放需要被踢下线的用户列表
		usersToKick := []UserEntity{}

		this.MapLock.RLock()
		for _, user := range this.OnlineMap {
			// 检查用户是否超时
			if time.Since(user.GetLastHeartbeat()) > HeartbeatTimeout {
				usersToKick = append(usersToKick, user)
			}
		}
		this.MapLock.RUnlock()

		// 遍历列表，执行踢人操作
		for _, user := range usersToKick {
			log.Printf("用户 [%s] 因心跳超时被服务器主动断开连接。", user.GetName())
			// 调用 user.Offline() 会处理所有下线逻辑（离开房间、保存数据等）
			// Offline 内部有锁，可以安全地并发调用
			user.Offline()
		}
	}
}

// RunLoop 负责所有的【写】操作，确保写的安全性
func (this *Server) RunLoop() {
	for req := range this.Requests {
		switch req.ReqType {
		case ReqTypeRegister:
			// 写操作，加锁
			this.MapLock.Lock()
			this.OnlineMap[req.User.GetName()] = req.User
			this.MapLock.Unlock()
			// 同步用户路由到 Redis（解决多实例下“本机 map 不可见”问题）
			storage.SetUserRoute(req.User.GetName(), this.InstanceID, this.routeTTL)

			broadcastMsg := "[" + req.User.GetAddr() + "]" + req.User.GetName() + " 上线啦 (全局消息)"
			fmt.Println(broadcastMsg)

			// 广播时只读，加读锁

			this.sendBroadcastToAll(broadcastMsg)

		case ReqTypeUnregister:
			this.MapLock.Lock()
			if _, ok := this.OnlineMap[req.User.GetName()]; ok {
				delete(this.OnlineMap, req.User.GetName())
				this.MapLock.Unlock() // 先解锁，再广播
				storage.DelUserRoute(req.User.GetName())

				broadcastMsg := "[" + req.User.GetAddr() + "]" + req.User.GetName() + " 下线了 (全局消息)"
				fmt.Println(broadcastMsg)

				this.sendBroadcastToAll(broadcastMsg)
			} else {
				this.MapLock.Unlock()
			}

		case ReqTypeBroadcast:
			this.MapLock.RLock()
			for _, user := range this.OnlineMap {
				user.Send(req.Msg)
			}
			this.MapLock.RUnlock()
		}
	}
}

// RefreshRoutesLoop 定期续约 Redis 路由，防止进程正常运行但路由因TTL过期消失。
func (this *Server) RefreshRoutesLoop() {
	interval := this.routeTTL / 3
	if interval < 3*time.Second {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		this.MapLock.RLock()
		for _, user := range this.OnlineMap {
			storage.SetUserRoute(user.GetName(), this.InstanceID, this.routeTTL)
		}
		this.MapLock.RUnlock()
	}
}

// RefreshGatewayLoop 定期续约本网关地址路由，供异机转发查询。
func (this *Server) RefreshGatewayLoop() {
	interval := this.routeTTL / 3
	if interval < 3*time.Second {
		interval = 3 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	storage.SetGatewayRoute(this.InstanceID, this.ForwardPublicAddr, this.routeTTL)

	for range ticker.C {
		storage.SetGatewayRoute(this.InstanceID, this.ForwardPublicAddr, this.routeTTL)
	}
}

func (this *Server) Handler(conn net.Conn) {
	if userFactory == nil {
		log.Fatal("UserFactory 未设置，无法创建用户连接处理器")
	}
	user := userFactory(conn, this)
	if user == nil {
		_ = conn.Close()
		return
	}
	user.Online()
	defer user.Offline()

	const KeepAliveDuration = 30 * time.Second

	for {
		_ = conn.SetReadDeadline(time.Now().Add(KeepAliveDuration))
		head := make([]byte, 2)
		if _, err := io.ReadFull(conn, head); err != nil {
			return
		}
		msgLen := binary.BigEndian.Uint16(head)
		body := make([]byte, msgLen)
		if _, err := io.ReadFull(conn, body); err != nil {
			return
		}
		user.DoMessage(string(body))
	}
}

func (this *Server) sendBroadcastToAll(msg string) {
	packedMsg := protocol.PackTextMessage(msg)
	this.MapLock.RLock()
	for _, user := range this.OnlineMap {
		user.Send(packedMsg)
	}
	this.MapLock.RUnlock()
}

func (this *Server) BroadCast(user UserEntity, msg string) {
	sendMsg := "[" + user.GetAddr() + "]" + user.GetName() + ":" + msg
	req := Request{
		ReqType: ReqTypeBroadcast,
		User:    user,
		Msg:     protocol.PackTextMessage(sendMsg),
	}
	this.Requests <- req
}

