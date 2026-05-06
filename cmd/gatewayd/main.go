package main

import (
	"context"
	"log"
	"net"
	"os"

	"github.com/mwitkow/grpc-proxy/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
)

const (
	gatewayPort = ":8080"
)

func main() {
	grpcBackendHost := os.Getenv("GRPC_BACKEND_HOST")
	if grpcBackendHost == "" {
		grpcBackendHost = "127.0.0.1:9090"
	}
	lis, err := net.Listen("tcp", gatewayPort)
	if err != nil {
		log.Fatalf("网关监听失败: %v", err)

	}

	// 【修正1】使用新的 grpc.NewClient API，替换掉被弃用的 DialContext
	backendConn, err := grpc.NewClient(
		grpcBackendHost,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		log.Fatalf("无法连接到后端 gRPC 服务: %v", err)
	}

	// 【修正2】将 director 函数的返回类型精确匹配 StreamDirector 的要求
	director := func(ctx context.Context, fullMethodName string) (context.Context, grpc.ClientConnInterface, error) {
		md, _ := metadata.FromIncomingContext(ctx)
		outCtx := metadata.NewOutgoingContext(ctx, md.Copy())

		// backendConn 是 *grpc.ClientConn 类型，它实现了 grpc.ClientConnInterface 接口，所以可以直接返回
		return outCtx, backendConn, nil
	}

	// 创建 gRPC 服务器作为网关
	// 【修正3】由于 director 的签名现在完全匹配，我们不再需要强制类型转换
	server := grpc.NewServer(
		grpc.UnknownServiceHandler(proxy.TransparentHandler(director)),
	)

	log.Printf("gRPC 透明代理网关正在启动，监听端口 %s...", gatewayPort)
	log.Printf("  -> 所有 gRPC 流量将被转发到 %s", grpcBackendHost)

	if err := server.Serve(lis); err != nil {
		log.Fatalf("网关启动失败: %v", err)
	}
}
