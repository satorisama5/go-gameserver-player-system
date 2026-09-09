// Package tool 提供本地开发用维护脚本（勿用于生产）。
package tool

import (
	"context"
	"fmt"
	"log"
	"time"

	cfg "unityserverupgrade/internal/config"
	"unityserverupgrade/internal/storage"

	"go.mongodb.org/mongo-driver/bson"
)

// ClearDevDatabase 清空本机演示用 Mongo 业务集合 + Redis DB0（升级5 后需重建 accounts/users）。
// 调用前需已执行 config.InitConfig、storage.InitRedis、storage.InitDB。
func ClearDevDatabase() error {
	if storage.DB == nil {
		return fmt.Errorf("MongoDB 未初始化，请先 InitDB")
	}
	if storage.RDB == nil {
		return fmt.Errorf("Redis 未初始化，请先 InitRedis")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	mongoCollections := []string{
		"users",
		"accounts",
		"friends",
		"friend_chat_logs",
		"room_chat_logs",
		"private_chat_logs",
		"wallet_accounts",
		"wallet_ledger",
		"kill_reward_outbox",
		"event_logs",
		"event_log_rollups",
		"blacklist",
		"inventories",
		"room_notes",
		"quest_progress",
	}

	log.Println("=== ClearDevDatabase 开始（仅本地演示）===")
	log.Printf("Mongo URI: %s  DB: UnityGameDB\n", cfg.Conf.Database.MongoURI)
	log.Printf("Redis: %s  DB=0 将执行 FLUSHDB\n", cfg.Conf.Cache.RedisAddr)

	for _, name := range mongoCollections {
		coll := storage.DB.Collection(name)
		res, err := coll.DeleteMany(ctx, bson.M{})
		if err != nil {
			return fmt.Errorf("Mongo 清空 %s 失败: %w", name, err)
		}
		log.Printf("[Mongo] %s: 删除 %d 条\n", name, res.DeletedCount)
	}

	if err := storage.RDB.FlushDB(ctx).Err(); err != nil {
		return fmt.Errorf("Redis FLUSHDB 失败: %w", err)
	}
	log.Println("[Redis] FLUSHDB 完成（含 player:*/idem:req:*/route:*/leaderboard 等）")
	log.Println("=== 清理完成：请重启 gamed，客户端用 register/login 重新注册 ===")
	return nil
}
