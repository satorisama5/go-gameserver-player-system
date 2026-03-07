// cmd/gamed/main.go (最终修正版，可直接替换)
package main

import (
	"fmt"
	"google.golang.org/grpc"
	"log"
	"net"
	"unityserverupgrade/internal" // 导入 internal 包
	"unityserverupgrade/proto"
)

// 这个函数现在只是一个简单的包装，调用 internal 包里的真正实现
func StartServer() {
	// 【修正】必须通过 internal 包名来调用
	server := internal.NewServer("0.0.0.0", internal.Conf.Server.TcpPort)
	server.Start()
}

func StartGrpcServer() {
	addr := fmt.Sprintf("0.0.0.0:%d", internal.Conf.Server.GrpcPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gRPC failed to listen: %v", err)
	}

	s := grpc.NewServer()

	proto.RegisterLeaderboardServiceServer(s, &internal.LeaderboardServer{})

	fmt.Println("gRPC 服务器正在监听 :9090...")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("gRPC failed to serve: %v", err)
	}
}

func main() {
	internal.InitConfig()
	// 【核心修正】所有对 internal 包内容的调用，都必须加上 "internal." 前缀
	internal.InitDB()
	internal.InitRedis()
	internal.InitMQ()
	internal.StartChatConsumer()

	internal.WordFilter = internal.NewFilterManager()
	sensitiveWords := internal.LoadSensitiveWords()
	internal.WordFilter.Build(sensitiveWords)
	
	go StartGrpcServer()

	go StartServer()

	//go internal.StartWebSocketProxy(8889, "127.0.0.1:8888")

	fmt.Println("Go 服务端 (TCP + gRPC) 已启动")

	select {}
}
