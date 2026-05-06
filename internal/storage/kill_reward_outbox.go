package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	KillRewardOutboxPending    = "PENDING"
	KillRewardOutboxWalletDone = "WALLET_DONE"
	KillRewardOutboxSuccess    = "SUCCESS"
)

// KillRewardOutbox 击杀奖励待办：先落库再推进，与 wallet_ledger(req_id) 幂等一致；榜分用 Redis req_id 幂等。
type KillRewardOutbox struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	UserKey    string             `bson:"user_key"`
	UserName   string             `bson:"user_name"`
	ReqID      string             `bson:"req_id"`
	KillID     string             `bson:"kill_id"`
	MonsterID  string             `bson:"monster_id"`
	GoldReward int64              `bson:"gold_reward"`
	ScoreDelta int32              `bson:"score_delta"`
	Status     string             `bson:"status"`
	LastErr    string             `bson:"last_err,omitempty"`
	TryCount   int                `bson:"try_count,omitempty"`
	CreatedAt  int64              `bson:"created_at"`
	UpdatedAt  int64              `bson:"updated_at"`
}

func EnsureKillRewardOutboxIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "user_key", Value: 1}, {Key: "req_id", Value: 1}},
			Options: options.Index().
				SetUnique(true).
				SetName("idx_kill_reward_outbox_user_req_unique"),
		},
		{
			Keys: bson.D{{Key: "status", Value: 1}, {Key: "updated_at", Value: 1}},
			Options: options.Index().
				SetName("idx_kill_reward_outbox_status_updated"),
		},
	}
	_, err := KillRewardOutboxCollection.Indexes().CreateMany(ctx, models)
	return err
}

// UpsertKillRewardOutboxPending 插入待处理记录；已存在同 (user_key, req_id) 则忽略（以首写为准）。
func UpsertKillRewardOutboxPending(userKey, userName, reqID, killID, monsterID string, gold int64, scoreDelta int32) error {
	if userKey == "" || reqID == "" {
		return fmt.Errorf("kill_reward_outbox: user_key/req_id 不能为空")
	}
	now := time.Now().Unix()
	doc := KillRewardOutbox{
		UserKey:    userKey,
		UserName:   userName,
		ReqID:      reqID,
		KillID:     killID,
		MonsterID:  monsterID,
		GoldReward: gold,
		ScoreDelta: scoreDelta,
		Status:     KillRewardOutboxPending,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := KillRewardOutboxCollection.InsertOne(ctx, doc)
	if err != nil && mongo.IsDuplicateKeyError(err) {
		return nil
	}
	return err
}

func findKillRewardOutbox(ctx context.Context, userKey, reqID string) (*KillRewardOutbox, error) {
	var doc KillRewardOutbox
	err := KillRewardOutboxCollection.FindOne(ctx, bson.M{"user_key": userKey, "req_id": reqID}).Decode(&doc)
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

func touchKillRewardOutboxErr(userKey, reqID, errmsg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, _ = KillRewardOutboxCollection.UpdateOne(ctx,
		bson.M{"user_key": userKey, "req_id": reqID},
		bson.M{
			"$set": bson.M{"last_err": errmsg, "updated_at": time.Now().Unix()},
			"$inc": bson.M{"try_count": 1},
		},
	)
}

// TryProcessKillRewardOutbox 推进单条 outbox：PENDING→钱包→WALLET_DONE→榜分→SUCCESS。钱包失败返回 error；榜分失败保持 WALLET_DONE 由后台再试。
func TryProcessKillRewardOutbox(userKey, reqID string) (finalStatus string, balance int64, walletDuplicate bool, procErr error) {
	const maxRounds = 6
	for round := 0; round < maxRounds; round++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		doc, err := findKillRewardOutbox(ctx, userKey, reqID)
		cancel()
		if err != nil {
			if err == mongo.ErrNoDocuments {
				return "", 0, false, fmt.Errorf("kill_reward_outbox 不存在: %s %s", userKey, reqID)
			}
			return "", 0, false, err
		}

		switch doc.Status {
		case KillRewardOutboxSuccess:
			balance, _ = GetWalletBalance(userKey)
			return KillRewardOutboxSuccess, balance, false, nil

		case KillRewardOutboxPending:
			_, dup, err := GrantKillRewardWithLedger(doc.UserKey, doc.UserName, doc.ReqID, doc.MonsterID, doc.GoldReward)
			if err != nil {
				touchKillRewardOutboxErr(userKey, reqID, err.Error())
				return KillRewardOutboxPending, 0, false, err
			}
			walletDuplicate = dup
			upCtx, upCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = KillRewardOutboxCollection.UpdateOne(upCtx,
				bson.M{"user_key": userKey, "req_id": reqID, "status": KillRewardOutboxPending},
				bson.M{"$set": bson.M{
					"status":     KillRewardOutboxWalletDone,
					"last_err":   "",
					"updated_at": time.Now().Unix(),
				}},
			)
			upCancel()
			continue

		case KillRewardOutboxWalletDone:
			err := ApplyLeaderboardScoreDeltaByReqID(doc.ReqID, doc.UserName, doc.ScoreDelta)
			if err != nil {
				touchKillRewardOutboxErr(userKey, reqID, err.Error())
				balance, _ = GetWalletBalance(userKey)
				return KillRewardOutboxWalletDone, balance, walletDuplicate, nil
			}
			finCtx, finCancel := context.WithTimeout(context.Background(), 5*time.Second)
			_, _ = KillRewardOutboxCollection.UpdateOne(finCtx,
				bson.M{"user_key": userKey, "req_id": reqID, "status": KillRewardOutboxWalletDone},
				bson.M{"$set": bson.M{
					"status":     KillRewardOutboxSuccess,
					"last_err":   "",
					"updated_at": time.Now().Unix(),
				}},
			)
			finCancel()
			balance, _ = GetWalletBalance(userKey)
			return KillRewardOutboxSuccess, balance, walletDuplicate, nil

		default:
			return "", 0, walletDuplicate, fmt.Errorf("kill_reward_outbox 未知状态: %s", doc.Status)
		}
	}
	balance, _ = GetWalletBalance(userKey)
	return KillRewardOutboxWalletDone, balance, walletDuplicate, nil
}

// StartKillRewardOutboxWorker 定时扫未完成的击杀奖励待办并重试推进。
func StartKillRewardOutboxWorker(interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for range ticker.C {
			runKillRewardOutboxBatch(120)
		}
	}()
}

func runKillRewardOutboxBatch(limit int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	filter := bson.M{"status": bson.M{"$in": bson.A{KillRewardOutboxPending, KillRewardOutboxWalletDone}}}
	opts := options.Find().SetSort(bson.D{{Key: "updated_at", Value: 1}}).SetLimit(limit)
	cur, err := KillRewardOutboxCollection.Find(ctx, filter, opts)
	if err != nil {
		log.Printf("kill_reward_outbox find: %v", err)
		return
	}
	defer cur.Close(ctx)
	for cur.Next(ctx) {
		var doc KillRewardOutbox
		if err := cur.Decode(&doc); err != nil {
			continue
		}
		_, _, _, _ = TryProcessKillRewardOutbox(doc.UserKey, doc.ReqID)
	}
	if err := cur.Err(); err != nil {
		log.Printf("kill_reward_outbox cursor: %v", err)
	}
}
