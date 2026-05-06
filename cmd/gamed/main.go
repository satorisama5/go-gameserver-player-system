// cmd/gamed/main.go (最终修正版，可直接替换)
package main

import (
	"fmt"
	"log"
	"net"
	"time"
	cfg "unityserverupgrade/internal/config"
	grpcsvc "unityserverupgrade/internal/grpc"
	mq "unityserverupgrade/internal/mq"
	_ "unityserverupgrade/internal/session"
	storage "unityserverupgrade/internal/storage"
	tool "unityserverupgrade/internal/tool"
	"unityserverupgrade/internal/world"
	"unityserverupgrade/proto"

	grpcnet "google.golang.org/grpc"
)

// 这个函数现在只是一个简单的包装，调用 internal 包里的真正实现
func StartServer() {
	server := world.NewServer("0.0.0.0", cfg.Conf.Server.TcpPort)
	server.Start()
}

func StartGrpcServer() {
	addr := fmt.Sprintf("0.0.0.0:%d", cfg.Conf.Server.GrpcPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("gRPC failed to listen: %v", err)
	}

	s := grpcnet.NewServer()

	proto.RegisterLeaderboardServiceServer(s, &grpcsvc.LeaderboardServer{})
	proto.RegisterWalletServiceServer(s, &grpcsvc.WalletServer{})

	fmt.Println("gRPC 服务器正在监听 :9090...")
	if err := s.Serve(lis); err != nil {
		log.Fatalf("gRPC failed to serve: %v", err)
	}
}

func main() {
	cfg.InitConfig()
	storage.InitDB()
	storage.InitRedis()
	storage.StartKillRewardOutboxWorker(12 * time.Second)
	mq.InitMQ()
	mq.RunChatLogConsumer()

	tool.WordFilter = tool.NewFilterManager()
	sensitiveWords := storage.LoadSensitiveWords()
	tool.WordFilter.Build(sensitiveWords)

	go StartGrpcServer()

	go StartServer()

	storage.StartEventLogAnalytics()

	//go internal.StartWebSocketProxy(8889, "127.0.0.1:8888")

	fmt.Println("Go 服务端 (TCP + gRPC) 已启动")

	select {}
}
