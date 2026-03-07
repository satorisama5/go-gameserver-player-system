package internal

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	reqTypeRegister = iota
	reqTypeUnregister
	reqTypeBroadcast
)
const (
	HeartbeatTimeout = 30 * time.Second
)

type request struct {
	reqType int
	user    *User
	msg     []byte
}

type Server struct {
	Ip   string
	Port int

	// 【混合模式】保留 mapLock，主要用于保护读操作
	OnlineMap map[string]*User
	mapLock   sync.RWMutex

	Rooms    map[string]*Room
	roomLock sync.RWMutex

	requests chan request
}

func NewServer(ip string, port int) *Server {
	return &Server{
		Ip:        ip,
		Port:      port,
		OnlineMap: make(map[string]*User),
		Rooms:     make(map[string]*Room),
		requests:  make(chan request, 256),
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

	for {
		conn, err := listener.Accept() //关键代码
		if err != nil {
			fmt.Println("listener accept err:", err)
			continue
		}
		this.mapLock.RLock()
		currentConnections := len(this.OnlineMap)
		this.mapLock.RUnlock()

		if currentConnections >= Conf.Server.MaxConnections {
			log.Printf("连接被拒绝: 服务器已满 (%d/%d)", currentConnections, Conf.Server.MaxConnections)
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
		usersToKick := []*User{}

		this.mapLock.RLock()
		for _, user := range this.OnlineMap {
			// 检查用户是否超时
			if time.Since(user.lastHeartbeat) > HeartbeatTimeout {
				usersToKick = append(usersToKick, user)
			}
		}
		this.mapLock.RUnlock()

		// 遍历列表，执行踢人操作
		for _, user := range usersToKick {
			log.Printf("用户 [%s] 因心跳超时被服务器主动断开连接。", user.Name)
			// 调用 user.Offline() 会处理所有下线逻辑（离开房间、保存数据等）
			// Offline 内部有锁，可以安全地并发调用
			user.Offline()
		}
	}
}

// RunLoop 负责所有的【写】操作，确保写的安全性
func (this *Server) RunLoop() {
	for req := range this.requests {
		switch req.reqType {
		case reqTypeRegister:
			// 写操作，加锁
			this.mapLock.Lock()
			this.OnlineMap[req.user.Name] = req.user
			this.mapLock.Unlock()

			broadcastMsg := "[" + req.user.Addr + "]" + req.user.Name + " 上线啦 (全局消息)"
			fmt.Println(broadcastMsg)

			// 广播时只读，加读锁

			this.sendBroadcastToAll(broadcastMsg)

		case reqTypeUnregister:
			this.mapLock.Lock()
			if _, ok := this.OnlineMap[req.user.Name]; ok {
				delete(this.OnlineMap, req.user.Name)
				this.mapLock.Unlock() // 先解锁，再广播

				broadcastMsg := "[" + req.user.Addr + "]" + req.user.Name + " 下线了 (全局消息)"
				fmt.Println(broadcastMsg)

				this.sendBroadcastToAll(broadcastMsg)
			} else {
				this.mapLock.Unlock()
			}

		case reqTypeBroadcast:
			this.mapLock.RLock()
			for _, user := range this.OnlineMap {
				user.Send(req.msg)
			}
			this.mapLock.RUnlock()
		}
	}
}

func (this *Server) Handler(conn net.Conn) {
	user := NewUser(conn, this)
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
	packedMsg := PackTextMessage(msg)
	this.mapLock.RLock()
	for _, user := range this.OnlineMap {
		user.Send(packedMsg)
	}
	this.mapLock.RUnlock()
}

func (this *Server) BroadCast(user *User, msg string) {
	sendMsg := "[" + user.Addr + "]" + user.Name + ":" + msg
	req := request{
		reqType: reqTypeBroadcast,
		msg:     PackTextMessage(sendMsg),
	}
	this.requests <- req
}
