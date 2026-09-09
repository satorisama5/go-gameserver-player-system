// redis.go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
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

// GatewayMeta 实例对外可达信息 + 负载（写入 route:gateway:{id}）。
type GatewayMeta struct {
	InstanceID string `json:"instance_id"`
	Forward    string `json:"forward"`
	TCP        string `json:"tcp"`
	Online     int    `json:"online"`
	Max        int    `json:"max"`
}

// SetGatewayMeta 写入网关元数据（JSON）；兼容旧调用方仍可通过 GetGatewayRoute 取 forward。
func SetGatewayMeta(meta GatewayMeta, ttl time.Duration) {
	if meta.InstanceID == "" || meta.Forward == "" {
		return
	}
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	key := "route:gateway:" + meta.InstanceID
	body, err := json.Marshal(meta)
	if err != nil {
		fmt.Printf("SetGatewayMeta marshal 失败 instance=%s err=%v\n", meta.InstanceID, err)
		return
	}
	if err := RDB.Set(Ctx, key, string(body), ttl).Err(); err != nil {
		fmt.Printf("SetGatewayMeta 失败 instance=%s err=%v\n", meta.InstanceID, err)
	}
}

// SetGatewayRoute 兼容旧接口：仅写 forward（无 tcp/online 时用空值补齐）。
func SetGatewayRoute(instanceID, forwardPublicAddr string, ttl time.Duration) {
	SetGatewayMeta(GatewayMeta{InstanceID: instanceID, Forward: forwardPublicAddr}, ttl)
}

// parseGatewayMeta 支持 JSON 元数据或旧版纯 forward 字符串。
func parseGatewayMeta(instanceID, raw string) (GatewayMeta, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return GatewayMeta{}, false
	}
	var meta GatewayMeta
	if err := json.Unmarshal([]byte(raw), &meta); err == nil && meta.Forward != "" {
		if meta.InstanceID == "" {
			meta.InstanceID = instanceID
		}
		return meta, true
	}
	return GatewayMeta{InstanceID: instanceID, Forward: raw}, true
}

// GetGatewayMeta 读取实例元数据。
func GetGatewayMeta(instanceID string) (GatewayMeta, bool) {
	if instanceID == "" {
		return GatewayMeta{}, false
	}
	key := "route:gateway:" + instanceID
	v, err := RDB.Get(Ctx, key).Result()
	if err != nil {
		return GatewayMeta{}, false
	}
	return parseGatewayMeta(instanceID, v)
}

// GetGatewayRoute 获取网关 HTTP forward 地址（私聊/跨服天梯转发用）。
func GetGatewayRoute(instanceID string) (string, bool) {
	meta, ok := GetGatewayMeta(instanceID)
	if !ok || meta.Forward == "" {
		return "", false
	}
	return meta.Forward, true
}

// DelGatewayRoute 删除网关实例路由。
func DelGatewayRoute(instanceID string) {
	if instanceID == "" {
		return
	}
	key := "route:gateway:" + instanceID
	RDB.Del(Ctx, key)
}

// ListGatewayMetas 列出当前存活的实例元数据（依赖 TTL，过期键不会出现）。
func ListGatewayMetas() []GatewayMeta {
	var out []GatewayMeta
	var cursor uint64
	for {
		keys, next, err := RDB.Scan(Ctx, cursor, "route:gateway:*", 50).Result()
		if err != nil {
			break
		}
		for _, key := range keys {
			id := strings.TrimPrefix(key, "route:gateway:")
			if id == "" || id == key {
				continue
			}
			v, err := RDB.Get(Ctx, key).Result()
			if err != nil {
				continue
			}
			if meta, ok := parseGatewayMeta(id, v); ok {
				out = append(out, meta)
			}
		}
		cursor = next
		if cursor == 0 {
			break
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Online == out[j].Online {
			return out[i].InstanceID < out[j].InstanceID
		}
		return out[i].Online < out[j].Online
	})
	return out
}

// PickLeastLoadedGateway 选 online 最少的实例；excludeInstance 非空时优先避开本机（若仅有本机仍可返回本机）。
func PickLeastLoadedGateway(excludeInstance string) (GatewayMeta, bool) {
	list := ListGatewayMetas()
	if len(list) == 0 {
		return GatewayMeta{}, false
	}
	if excludeInstance == "" {
		return list[0], true
	}
	for _, m := range list {
		if m.InstanceID != excludeInstance {
			return m, true
		}
	}
	return list[0], true
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

// --- 房间笔记 Redis 热缓存（全文快照；权威仍以房间内存为准）---

const noteCacheTTL = 24 * time.Hour

func noteCacheKey(roomSessionID, noteID string) string {
	if noteID == "" {
		noteID = "raid"
	}
	return "note:snap:" + roomSessionID + ":" + noteID
}

// CacheRoomNoteSnapshot 写入 Redis 当前笔记快照（JSON）。
func CacheRoomNoteSnapshot(roomSessionID, noteID string, doc RoomNoteDoc) {
	if RDB == nil || roomSessionID == "" {
		return
	}
	if noteID == "" {
		noteID = "raid"
	}
	doc.RoomSessionID = roomSessionID
	doc.NoteID = noteID
	if doc.UpdatedAt == 0 {
		doc.UpdatedAt = time.Now().Unix()
	}
	body, err := json.Marshal(doc)
	if err != nil {
		return
	}
	_ = RDB.Set(Ctx, noteCacheKey(roomSessionID, noteID), body, noteCacheTTL).Err()
}

// GetCachedRoomNoteSnapshot 读取 Redis 笔记快照；未命中返回 nil。
func GetCachedRoomNoteSnapshot(roomSessionID, noteID string) *RoomNoteDoc {
	if RDB == nil || roomSessionID == "" {
		return nil
	}
	if noteID == "" {
		noteID = "raid"
	}
	s, err := RDB.Get(Ctx, noteCacheKey(roomSessionID, noteID)).Result()
	if err != nil {
		return nil
	}
	var doc RoomNoteDoc
	if json.Unmarshal([]byte(s), &doc) != nil {
		return nil
	}
	return &doc
}
