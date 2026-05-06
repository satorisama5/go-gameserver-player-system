package grpc

import (
	"context"
	"fmt"

	pb "unityserverupgrade/proto"

	storage "unityserverupgrade/internal/storage"
)

const (
	defaultWalletBalance = int64(1000)
	itemACost            = int64(100)
)

// WalletServer 提供钱包账本 + 商城扣费（最小版）能力。
type WalletServer struct {
	pb.UnimplementedWalletServiceServer
}

func (s *WalletServer) GetWallet(ctx context.Context, req *pb.GetWalletRequest) (*pb.GetWalletResponse, error) {
	if req.GetUserKey() == "" || req.GetUserName() == "" {
		return &pb.GetWalletResponse{
			Success: false,
			Message: "user_key 或 user_name 不能为空",
		}, nil
	}
	acc, err := storage.EnsureWalletAccount(req.GetUserKey(), req.GetUserName(), defaultWalletBalance)
	if err != nil {
		return &pb.GetWalletResponse{
			Success: false,
			Message: fmt.Sprintf("查询钱包失败: %v", err),
		}, nil
	}
	return &pb.GetWalletResponse{
		Success: true,
		Balance: acc.Balance,
		Message: "ok",
	}, nil
}

func (s *WalletServer) PurchaseItemA(ctx context.Context, req *pb.PurchaseItemARequest) (*pb.PurchaseItemAResponse, error) {
	if req.GetUserKey() == "" || req.GetUserName() == "" {
		return &pb.PurchaseItemAResponse{
			Success: false,
			ItemId:  "item_a",
			Cost:    itemACost,
			Message: "user_key 或 user_name 不能为空",
		}, nil
	}
	if req.GetReqId() == "" {
		return &pb.PurchaseItemAResponse{
			Success: false,
			ItemId:  "item_a",
			Cost:    itemACost,
			Message: "req_id 不能为空（用于幂等）",
		}, nil
	}

	if _, err := storage.EnsureWalletAccount(req.GetUserKey(), req.GetUserName(), defaultWalletBalance); err != nil {
		return &pb.PurchaseItemAResponse{
			Success: false,
			ItemId:  "item_a",
			Cost:    itemACost,
			Message: fmt.Sprintf("初始化钱包失败: %v", err),
		}, nil
	}

	balance, duplicate, err := storage.PurchaseItemAWithLedger(req.GetUserKey(), req.GetUserName(), req.GetReqId(), itemACost)
	if err != nil {
		msg := fmt.Sprintf("购买失败: %v", err)
		if err.Error() == "insufficient balance" {
			msg = "余额不足"
		}
		return &pb.PurchaseItemAResponse{
			Success:   false,
			Balance:   balance,
			ItemId:    "item_a",
			Cost:      itemACost,
			Duplicate: duplicate,
			Message:   msg,
		}, nil
	}

	respMsg := "购买成功"
	if duplicate {
		respMsg = "重复请求，已返回首次扣费结果"
	}
	return &pb.PurchaseItemAResponse{
		Success:   true,
		Balance:   balance,
		ItemId:    "item_a",
		Cost:      itemACost,
		Duplicate: duplicate,
		Message:   respMsg,
	}, nil
}

// GrantKillReward 击杀奖励：先写入 kill_reward_outbox(PENDING)，再 TryProcess（钱包事务 + Redis 榜分幂等）；未完成则由 StartKillRewardOutboxWorker 扫表重试。
func (s *WalletServer) GrantKillReward(ctx context.Context, req *pb.GrantKillRewardRequest) (*pb.GrantKillRewardResponse, error) {
	if req.GetUserKey() == "" || req.GetUserName() == "" {
		return &pb.GrantKillRewardResponse{
			Success: false,
			Message: "user_key 或 user_name 不能为空",
		}, nil
	}
	if req.GetReqId() == "" || req.GetKillId() == "" {
		return &pb.GrantKillRewardResponse{
			Success: false,
			Message: "req_id 和 kill_id 不能为空",
		}, nil
	}
	if req.GetGoldReward() <= 0 || req.GetScoreDelta() <= 0 {
		return &pb.GrantKillRewardResponse{
			Success: false,
			Message: "gold_reward 和 score_delta 必须为正数",
		}, nil
	}

	if err := storage.UpsertKillRewardOutboxPending(
		req.GetUserKey(),
		req.GetUserName(),
		req.GetReqId(),
		req.GetKillId(),
		req.GetMonsterId(),
		req.GetGoldReward(),
		req.GetScoreDelta(),
	); err != nil {
		return &pb.GrantKillRewardResponse{
			Success: false,
			Message: fmt.Sprintf("写入击杀奖励待办失败: %v", err),
		}, nil
	}

	status, balance, duplicate, err := storage.TryProcessKillRewardOutbox(req.GetUserKey(), req.GetReqId())
	if err != nil {
		return &pb.GrantKillRewardResponse{
			Success: false,
			Message: fmt.Sprintf("钱包入账失败: %v", err),
		}, nil
	}

	if status == storage.KillRewardOutboxSuccess {
		msg := "击杀奖励已入账并加分成功"
		if duplicate {
			msg = "重复请求，已返回首次处理结果"
		}
		return &pb.GrantKillRewardResponse{
			Success:   true,
			Balance:   balance,
			Duplicate: duplicate,
			Message:   msg,
		}, nil
	}

	bal, _ := storage.GetWalletBalance(req.GetUserKey())
	return &pb.GrantKillRewardResponse{
		Success:   true,
		Balance:   bal,
		Duplicate: duplicate,
		Message:   "金币已入账；排行榜加分由后台重试直至成功",
	}, nil
}
