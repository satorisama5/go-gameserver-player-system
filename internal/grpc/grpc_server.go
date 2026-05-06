// in internal/grpc_server.go
package grpc

import (
	"context"
	"fmt"
	pb "unityserverupgrade/proto"

	storage "unityserverupgrade/internal/storage"
)

// 【核心修正】将 'l' 改为大写 'L'，将其导出
type LeaderboardServer struct {
	pb.UnimplementedLeaderboardServiceServer
}

// 【对应修正】方法的接收者也要改成大写
func (s *LeaderboardServer) SubmitScore(ctx context.Context, req *pb.SubmitScoreRequest) (*pb.SubmitScoreResponse, error) {
	// 语义为增量加分（ZIncrBy），供 Wallet.GrantKillReward 在击杀奖励入账后调用；非“覆盖总分”。
	fmt.Printf("收到 SubmitScore 请求(增量): Player=%s, Delta=%d\n", req.PlayerName, req.Score)
	err := storage.ApplyLeaderboardScoreDelta(req.PlayerName, req.Score)
	if err != nil {
		return &pb.SubmitScoreResponse{Success: false}, err
	}
	return &pb.SubmitScoreResponse{Success: true}, nil
}

// 【对应修正】方法的接收者也要改成大写
func (s *LeaderboardServer) GetTopPlayers(ctx context.Context, req *pb.GetTopPlayersRequest) (*pb.GetTopPlayersResponse, error) {
	// ... 方法内部逻辑完全不变 ...
	fmt.Printf("收到 GetTopPlayers 请求: Limit=%d\n", req.Limit)
	results, err := storage.RDB.ZRevRangeWithScores(storage.Ctx, "leaderboard", 0, int64(req.Limit-1)).Result()
	if err != nil {
		return nil, err
	}
	response := &pb.GetTopPlayersResponse{}
	for i, member := range results {
		response.Players = append(response.Players, &pb.PlayerRankInfo{
			Rank:       int32(i + 1),
			PlayerName: member.Member.(string),
			Score:      int32(member.Score),
		})
	}
	return response, nil
}
