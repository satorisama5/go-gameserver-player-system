// redis.go
package storage

import (
	"context"
	"fmt"
	"strconv"
	"time"

	cfg "unityserverupgrade/internal/config"

	"github.com/go-redis/redis/v8"
)

var RDB *redis.Client
var Ctx = context.Background()

// 每用户每秒允许的消息条数（仅聊天类接口）
const messagesPerSecond = 10

func InitRedis() {
	addr := cfg.Conf.Cache.RedisAddr

	RDB = redis.NewClient(&redis.Options{
		Addr:     addr, // Redis 服务器地址
		Password: "",   // 没有密码，填空
		DB:       0,    // 使用默认 DB
	})

	// 检查连接
	_, err := RDB.Ping(Ctx).Result()
	if err != nil {
		fmt.Println("Redis 连接失败:", err)
		panic(err)
	}

	fmt.Println("Redis 连接成功！")
}

const leaderboardRedisKey = "leaderboard"

// ApplyLeaderboardScoreDelta 对排行榜 ZSet 做增量（与 Leaderboard gRPC SubmitScore 一致，无 req_id 幂等）。
func ApplyLeaderboardScoreDelta(playerName string, delta int32) error {
	if playerName == "" || delta == 0 {
		return nil
	}
	return RDB.ZIncrBy(Ctx, leaderboardRedisKey, float64(delta), playerName).Err()
}

// ApplyLeaderboardScoreDeltaByReqID 击杀奖励等场景：同一 req_id 仅对排行榜加一次分（SETNX + ZIncrBy）。
// 若 ZIncrBy 失败会删除标记键以便重试。非「回滚钱包」，仅避免重复加分。
func ApplyLeaderboardScoreDeltaByReqID(reqID, playerName string, delta int32) error {
	if reqID == "" {
		return ApplyLeaderboardScoreDelta(playerName, delta)
	}
	if playerName == "" || delta == 0 {
		return nil
	}
	markKey := "lboard:score_applied:" + reqID
	ok, err := RDB.SetNX(Ctx, markKey, "1", 48*time.Hour).Result()
	if err != nil {
		return err
	}
	if !ok {
		return nil
	}
	if err := RDB.ZIncrBy(Ctx, leaderboardRedisKey, float64(delta), playerName).Err(); err != nil {
		_, _ = RDB.Del(Ctx, markKey).Result()
		return err
	}
	return nil
}

// AllowMessage 使用 Redis 固定窗口限流（纯 Go，无 Lua），每用户每秒最多 messagesPerSecond 条消息；允许返回 true，超限返回 false。
// key = ratelimit:msg:{userKey}:{当前秒}，INCR 后若为 1 则设置 EXPIRE 2s，计数≤10 则放行。
func AllowMessage(userKey string) bool {
	if userKey == "" {
		return false
	}
	now := time.Now().Unix()
	key := "ratelimit:msg:" + userKey + ":" + strconv.FormatInt(now, 10)
	n, err := RDB.Incr(Ctx, key).Result()
	if err != nil {
		return false
	}
	if n == 1 {
		RDB.Expire(Ctx, key, 2*time.Second)
	}
	return n <= int64(messagesPerSecond)
}

// TryRenameLock 对 newName 加分布式锁，key=lock:rename:{newName}，TTL=3s；获取成功返回 true。
func TryRenameLock(newName string) bool {
	key := "lock:rename:" + newName
	ok, err := RDB.SetNX(Ctx, key, "1", 3*time.Second).Result()
	if err != nil {
		return false
	}
	return ok
}

// UnlockRename 释放重命名锁。
func UnlockRename(newName string) {
	key := "lock:rename:" + newName
	RDB.Del(Ctx, key)
}

// SetUserRoute 设置用户到网关实例的路由，key=route:user:{userName}
func SetUserRoute(userName, instanceID string, ttl time.Duration) {
	if userName == "" || instanceID == "" {
		return
	}
	key := "route:user:" + userName
	if err := RDB.Set(Ctx, key, instanceID, ttl).Err(); err != nil {
		fmt.Printf("SetUserRoute 失败 user=%s err=%v\n", userName, err)
	}
}

// GetUserRoute 获取用户路由到的网关实例ID。
func GetUserRoute(userName string) (string, bool) {
	if userName == "" {
		return "", false
	}
	key := "route:user:" + userName
	v, err := RDB.Get(Ctx, key).Result()
	if err != nil {
		return "", false
	}
	return v, true
}

// DelUserRoute 删除用户路由。
func DelUserRoute(userName string) {
	if userName == "" {
		return
	}
	key := "route:user:" + userName
	RDB.Del(Ctx, key)
}

// SetGatewayRoute 设置网关实例到可访问地址的路由，key=route:gateway:{instanceID}
func SetGatewayRoute(instanceID, forwardPublicAddr string, ttl time.Duration) {
	if instanceID == "" || forwardPublicAddr == "" {
		return
	}
	key := "route:gateway:" + instanceID
	if err := RDB.Set(Ctx, key, forwardPublicAddr, ttl).Err(); err != nil {
		fmt.Printf("SetGatewayRoute 失败 instance=%s err=%v\n", instanceID, err)
	}
}

// GetGatewayRoute 获取网关实例的可访问地址。
func GetGatewayRoute(instanceID string) (string, bool) {
	if instanceID == "" {
		return "", false
	}
	key := "route:gateway:" + instanceID
	v, err := RDB.Get(Ctx, key).Result()
	if err != nil {
		return "", false
	}
	return v, true
}

// DelGatewayRoute 删除网关实例路由。
func DelGatewayRoute(instanceID string) {
	if instanceID == "" {
		return
	}
	key := "route:gateway:" + instanceID
	RDB.Del(Ctx, key)
}

// NextFriendConversationSeq 为好友会话分配递增序号。
func NextFriendConversationSeq(conversationID string) int64 {
	if conversationID == "" {
		return 0
	}
	key := "chatseq:friend:" + conversationID
	seq, err := RDB.Incr(Ctx, key).Result()
	if err != nil {
		return 0
	}
	// 长时间无消息自动过期，避免键无限增长
	_ = RDB.Expire(Ctx, key, 30*24*time.Hour).Err()
	return seq
}

// MarkReqIDIfNew 使用 req_id 做幂等查重：首次请求返回 true，重复请求返回 false。
// key = idem:req:{userKey}:{reqID}，TTL 建议短时（如 2 分钟）用于防止客户端重发。
func MarkReqIDIfNew(userKey, reqID string, ttl time.Duration) bool {
	if userKey == "" || reqID == "" {
		return true // 缺少幂等字段时走兼容放行
	}
	key := "idem:req:" + userKey + ":" + reqID
	ok, err := RDB.SetNX(Ctx, key, "1", ttl).Result()
	if err != nil {
		// Redis 异常时 fail-open，避免影响主流程可用性
		return true
	}
	return ok
}
