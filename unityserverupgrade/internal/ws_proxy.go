package internal

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true }, // 允许跨域
}

// 启动 WebSocket 代理
func StartWebSocketProxy(wsPort int, tcpAddr string) {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handleWS(w, r, tcpAddr)
	})

	log.Printf("WebSocket 代理启动: :%d -> %s", wsPort, tcpAddr)
	if err := http.ListenAndServe(fmt.Sprintf(":%d", wsPort), nil); err != nil {
		log.Fatal("WS 启动失败:", err)
	}
}

func handleWS(w http.ResponseWriter, r *http.Request, tcpAddr string) {
	// 1. 升级 HTTP 为 WebSocket
	wsConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer wsConn.Close()

	// 2. 连接本地 TCP 游戏服
	tcpConn, err := net.Dial("tcp", tcpAddr)
	if err != nil {
		log.Println("连接 TCP 失败:", err)
		return
	}
	defer tcpConn.Close()

	// 3. 开始双向转发
	errChan := make(chan error, 2)

	// A. WS -> TCP (加上 2字节长度头)
	go func() {
		for {
			_, msg, err := wsConn.ReadMessage()
			if err != nil {
				errChan <- err
				return
			}
			// 封装协议头
			header := make([]byte, 2)
			binary.BigEndian.PutUint16(header, uint16(len(msg)))

			// 发送头 + 体
			tcpConn.Write(header)
			tcpConn.Write(msg)
		}
	}()

	// B. TCP -> WS (去掉 2字节长度头)
	go func() {
		header := make([]byte, 2)
		for {
			// 读头
			if _, err := io.ReadFull(tcpConn, header); err != nil {
				errChan <- err
				return
			}
			length := binary.BigEndian.Uint16(header)

			// 读体
			body := make([]byte, length)
			if _, err := io.ReadFull(tcpConn, body); err != nil {
				errChan <- err
				return
			}

			// 直接发给 WS (WS 协议自带分包，不需要头)
			if err := wsConn.WriteMessage(websocket.TextMessage, body); err != nil {
				errChan <- err
				return
			}
		}
	}()

	<-errChan // 等待任意一方断开
}
