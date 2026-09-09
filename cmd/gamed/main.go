package main

import (
	"fmt"
	"log"
	"net"
	"time"

	cfg "unityserverupgrade/internal/config"
	grpcsvc "unityserverupgrade/internal/grpc"
	mq "unityserverupgrade/internal/mq"
	_ "unityserverupgrade/internal/session" // 触发 init：注入 UserFactory，打断 world↔session 循环依赖
	storage "unityserverupgrade/internal/storage"
	tool "unityserverupgrade/internal/tool"
	"unityserverupgrade/internal/world"
	"unityserverupgrade/proto"

	grpcnet "google.golang.org/grpc"
)

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
	mq.RunNoteSnapshotConsumer()

	tool.WordFilter = tool.NewFilterManager()
	sensitiveWords := storage.LoadSensitiveWords()
	tool.WordFilter.Build(sensitiveWords)

	go StartGrpcServer()
	go StartServer()
	storage.StartEventLogAnalytics()

	fmt.Println("Go 服务端 (TCP + gRPC) 已启动")
	select {}
}
