// db.go
package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	cfg "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
	"unityserverupgrade/internal/storage/inventory"
)

const (
	playerCacheKeyPrefix = "player:"
	playerCacheTTL       = 10 * time.Minute
	playerEmptyTTL       = 1 * time.Minute
	playerEmptyMarker    = "__empty__"
)

var DB *mongo.Database
var UserCollection *mongo.Collection
var BlacklistCollection *mongo.Collection
var RoomChatCollection *mongo.Collection
var PrivateChatCollection *mongo.Collection
var FriendCollection *mongo.Collection
var FriendChatCollection *mongo.Collection
var WalletCollection *mongo.Collection
var WalletLedgerCollection *mongo.Collection
var EventLogCollection *mongo.Collection
var EventLogRollupCollection *mongo.Collection
var KillRewardOutboxCollection *mongo.Collection
var InventoryCollection *mongo.Collection

// DefaultWalletBalance 新钱包账户默认余额（演示）。
const DefaultWalletBalance = int64(1000)

var DefaultSensitiveWords = []string{
	"外挂",
	"傻",
	"fuck",
	"傻逼",
	"垃圾",
	"GM",
	"死",
	"妈",
	"全家",
}

// 定义存入数据库的玩家结构 (Model)
type PlayerModel struct {
	UniqueKey string                   `bson:"unique_key"`
	Username  string                   `bson:"username"`
	LastIp    string                   `bson:"last_ip"`
	PositionX float32                  `bson:"pos_x"`
	PositionY float32                  `bson:"pos_y"`
	PositionZ float32                  `bson:"pos_z"`
	UpdatedAt int64                    `bson:"updated_at"`
	LastScene string                   `bson:"last_scene"`
	Attrs     protocol.CharacterAttrs  `bson:"attrs,omitempty"`
}

type RoomChatLog struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	RoomID    string             `bson:"room_id"`
	Sender    string             `bson:"sender"`
	Message   string             `bson:"message"` // 存过滤后的内容
	Timestamp int64              `bson:"timestamp"`
}

type PrivateChatLog struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	ConversationID string             `bson:"conversation_id"`
	Sender         string             `bson:"sender"`
	Receiver       string             `bson:"receiver"`
	Message        string             `bson:"message"` // 存过滤后的内容
	Timestamp      int64              `bson:"timestamp"`
}

type FriendRelation struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	OwnerKey   string             `bson:"owner_key"`
	OwnerName  string             `bson:"owner_name"`
	FriendKey  string             `bson:"friend_key"`
	FriendName string             `bson:"friend_name"`
	CreatedAt  int64              `bson:"created_at"`
}

type FriendChatLog struct {
	ID             primitive.ObjectID `bson:"_id,omitempty"`
	ConversationID string             `bson:"conversation_id"`
	Seq            int64              `bson:"seq"`
	Sender         string             `bson:"sender"`
	Receiver       string             `bson:"receiver"`
	Message        string             `bson:"message"`
	Timestamp      int64              `bson:"timestamp"`
}

type WalletAccount struct {
	ID        primitive.ObjectID `bson:"_id,omitempty"`
	UserKey   string             `bson:"user_key"`
	UserName  string             `bson:"user_name"`
	Balance   int64              `bson:"balance"`
	UpdatedAt int64              `bson:"updated_at"`
}

type WalletLedger struct {
	ID            primitive.ObjectID `bson:"_id,omitempty"`
	UserKey       string             `bson:"user_key"`
	UserName      string             `bson:"user_name"`
	ReqID         string             `bson:"req_id"`
	ItemID        string             `bson:"item_id"`
	Cost          int64              `bson:"cost"`
	BalanceBefore int64              `bson:"balance_before"`
	BalanceAfter  int64              `bson:"balance_after"`
	CreatedAt     int64              `bson:"created_at"`
}

type EventLog struct {
	ID         primitive.ObjectID `bson:"_id,omitempty"`
	EventType  string             `bson:"event_type"`
	Level      string             `bson:"level"`
	ReqID      string             `bson:"req_id,omitempty"`
	UserKey    string             `bson:"user_key,omitempty"`
	UserName   string             `bson:"user_name,omitempty"`
	SessionID  string             `bson:"session_id,omitempty"`
	InstanceID string             `bson:"instance_id,omitempty"`
	Message    string             `bson:"message"`
	Meta       bson.M             `bson:"meta,omitempty"`
	CreatedAt  int64              `bson:"created_at"`
}

func InitDB() {
	// 设置连接超时
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoURI := cfg.Conf.Database.MongoURI
	clientOptions := options.Client().ApplyURI(mongoURI)
	client, err := mongo.Connect(ctx, clientOptions)
	if err != nil {
		log.Fatal("数据库连接失败:", err)
	}

	// 检查连接
	err = client.Ping(ctx, nil)
	if err != nil {
		log.Fatal("数据库无法 Ping 通:", err)
	}

	fmt.Println("MongoDB 连接成功！")

	// 获取数据库和集合
	DB = client.Database("UnityGameDB")
	UserCollection = DB.Collection("users")
	BlacklistCollection = DB.Collection("blacklist")
	RoomChatCollection = DB.Collection("room_chat_logs")
	PrivateChatCollection = DB.Collection("private_chat_logs")
	FriendCollection = DB.Collection("friends")
	FriendChatCollection = DB.Collection("friend_chat_logs")
	WalletCollection = DB.Collection("wallet_accounts")
	WalletLedgerCollection = DB.Collection("wallet_ledger")
	EventLogCollection = DB.Collection("event_logs")
	EventLogRollupCollection = DB.Collection("event_log_rollups")
	KillRewardOutboxCollection = DB.Collection("kill_reward_outbox")
	AccountCollection = DB.Collection("accounts")
	InventoryCollection = DB.Collection("inventories")
	InitRoomNoteCollection()
	InitQuestCollection()
	// 索引优化：用于幂等唯一约束 + 加速按 user_key / req_id 查询
	if err := EnsureWalletIndexes(); err != nil {
		log.Println("wallet 索引创建失败(不影响启动，但可能影响幂等与性能):", err)
	}
	if err := EnsureKillRewardOutboxIndexes(); err != nil {
		log.Println("kill_reward_outbox 索引创建失败(不影响启动，但影响奖励补偿扫描):", err)
	}
	if err := EnsureEventLogIndexes(); err != nil {
		log.Println("event_logs 索引创建失败(不影响启动，但会影响排障查询效率):", err)
	}
	if err := EnsureEventLogRollupIndexes(); err != nil {
		log.Println("event_log_rollups 索引创建失败(不影响启动，但会影响聚合写入):", err)
	}
	if err := EnsureAccountIndexes(); err != nil {
		log.Println("accounts 索引创建失败(不影响启动，但会影响注册登录):", err)
	}
	if err := inventory.Init(InventoryCollection); err != nil {
		log.Println("inventories 索引创建失败(不影响启动，但会影响背包):", err)
	}
}

// EnsureWalletIndexes creates the minimal indexes required for:
// 1) wallet_accounts: fast lookup by user_key
// 2) wallet_ledger: enforce (user_key, req_id) uniqueness for idempotency
func EnsureWalletIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// wallet_accounts.user_key unique
	accIdx := mongo.IndexModel{
		Keys: bson.D{{Key: "user_key", Value: 1}},
		Options: options.Index().
			SetUnique(true).
			SetName("idx_wallet_accounts_user_key_unique"),
	}
	if _, err := WalletCollection.Indexes().CreateOne(ctx, accIdx); err != nil {
		return err
	}

	// wallet_ledger: (user_key, req_id) unique
	ledgerIdx := mongo.IndexModel{
		Keys: bson.D{
			{Key: "user_key", Value: 1},
			{Key: "req_id", Value: 1},
		},
		Options: options.Index().
			SetUnique(true).
			SetName("idx_wallet_ledger_user_req_unique"),
	}
	if _, err := WalletLedgerCollection.Indexes().CreateOne(ctx, ledgerIdx); err != nil {
		return err
	}

	return nil
}

// EnsureEventLogIndexes creates indexes for fast troubleshooting queries.
func EnsureEventLogIndexes() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	idxes := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_created_at_desc"),
		},
		{
			Keys: bson.D{{Key: "req_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_req_created"),
		},
		{
			Keys: bson.D{{Key: "user_key", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_user_created"),
		},
	}
	_, err := EventLogCollection.Indexes().CreateMany(ctx, idxes)
	return err
}

func SaveEventLog(entry EventLog) {
	if EventLogCollection == nil {
		return
	}
	if entry.EventType == "" {
		entry.EventType = "unknown"
	}
	if entry.Level == "" {
		entry.Level = "info"
	}
	if entry.CreatedAt == 0 {
		entry.CreatedAt = time.Now().Unix()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := EventLogCollection.InsertOne(ctx, entry); err != nil {
		log.Printf("保存事件日志失败: %v", err)
	}
}

func SaveEventLogAsync(entry EventLog) {
	go SaveEventLog(entry)
}

// 辅助函数：保存/更新玩家数据（含角色属性）
func SavePlayerToDB(uniqueKey string, name string, pos protocol.PlayerPosition, roomName string, attrs protocol.CharacterAttrs) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if attrs.Level == 0 && attrs.MaxHP == 0 {
		attrs = protocol.DefaultCharacterAttrs()
	}

	filter := bson.M{"unique_key": uniqueKey}
	update := bson.M{
		"$set": bson.M{
			"username":   name,
			"pos_x":      pos.X,
			"pos_y":      pos.Y,
			"pos_z":      pos.Z,
			"last_scene": roomName,
			"updated_at": time.Now().Unix(),
			"attrs":      attrs,
		},
	}
	opts := options.Update().SetUpsert(true)

	_, err := UserCollection.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		fmt.Println("保存玩家数据失败:", err)
		return
	}
	// 写库后刷新缓存
	InvalidatePlayerCache(uniqueKey)
}

// InvalidatePlayerCache 清玩家 Redis 缓存，避免属性过期读旧值。
func InvalidatePlayerCache(uniqueKey string) {
	if RDB == nil || uniqueKey == "" {
		return
	}
	_ = RDB.Del(Ctx, playerCacheKeyPrefix+uniqueKey).Err()
}

func LoadPlayerFromDB(uniqueKey string) (*PlayerModel, bool) {
	cacheKey := playerCacheKeyPrefix + uniqueKey

	// 先查 Redis 缓存
	val, err := RDB.Get(Ctx, cacheKey).Result()
	if err == nil {
		if val == playerEmptyMarker {
			return nil, false
		}
		var result PlayerModel
		if json.Unmarshal([]byte(val), &result) == nil {
			return &result, true
		}
	}

	// 未命中则查 MongoDB
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result PlayerModel
	filter := bson.M{"unique_key": uniqueKey}
	err = UserCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		// MongoDB 也没有则写空值缓存防穿透，TTL=1min
		RDB.Set(Ctx, cacheKey, playerEmptyMarker, playerEmptyTTL)
		return nil, false
	}

	// 命中 MongoDB，写缓存 TTL=10min
	if data, e := json.Marshal(&result); e == nil {
		RDB.Set(Ctx, cacheKey, string(data), playerCacheTTL)
	}
	return &result, true
}

func DefaultWordsToBson() []interface{} {
	arr := make([]interface{}, 0, len(DefaultSensitiveWords))
	for _, w := range DefaultSensitiveWords {
		arr = append(arr, bson.M{"word": w})
	}
	return arr
}

func LoadSensitiveWords() []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := BlacklistCollection.Find(ctx, bson.M{})
	if err != nil {
		fmt.Println("加载黑名单失败 (可能为空):", err)
		return DefaultSensitiveWords
	}
	defer cursor.Close(ctx)

	var words []string
	for cursor.Next(ctx) {
		var result struct {
			Word string `bson:"word"`
		}
		if err := cursor.Decode(&result); err == nil {
			words = append(words, result.Word)
		}
	}

	// 如果数据库内容为空，写入默认词库
	if len(words) == 0 {
		fmt.Println("检测到数据库黑名单为空，正在初始化默认词库...")
		_, err := BlacklistCollection.InsertMany(ctx, DefaultWordsToBson())
		if err != nil {
			fmt.Println("默认词库写入失败:", err)
		}
		return DefaultSensitiveWords
	}

	fmt.Printf("成功加载敏感词库: %d 个词\n", len(words))
	return words
}

func SaveRoomChatLog(roomID, sender, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logEntry := RoomChatLog{
		RoomID:    roomID,
		Sender:    sender,
		Message:   msg,
		Timestamp: time.Now().Unix(),
	}

	_, err := RoomChatCollection.InsertOne(ctx, logEntry)
	if err != nil {
		fmt.Println("保存房间聊天记录失败:", err)
		return err
	}
	return nil
}

func SavePrivateChatLog(conversationID, sender, receiver, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logEntry := PrivateChatLog{
		ConversationID: conversationID,
		Sender:         sender,
		Receiver:       receiver,
		Message:        msg,
		Timestamp:      time.Now().Unix(),
	}

	_, err := PrivateChatCollection.InsertOne(ctx, logEntry)
	if err != nil {
		fmt.Println("保存私聊记录失败:", err)
		return err
	}
	return nil
}

func BuildFriendConversationID(userKeyA, userKeyB string) string {
	if userKeyA < userKeyB {
		return "FRIEND_" + userKeyA + "_" + userKeyB
	}
	return "FRIEND_" + userKeyB + "_" + userKeyA
}

func IsUserIdentityMatched(uniqueKey, userName string) bool {
	if uniqueKey == "" || userName == "" {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"unique_key": uniqueKey, "username": userName}
	err := UserCollection.FindOne(ctx, filter).Err()
	return err == nil
}

func AddFriend(ownerKey, ownerName, friendKey, friendName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"owner_key": ownerKey, "friend_key": friendKey}
	update := bson.M{
		"$set": bson.M{
			"owner_key":   ownerKey,
			"owner_name":  ownerName,
			"friend_key":  friendKey,
			"friend_name": friendName,
			"created_at":  time.Now().Unix(),
		},
	}
	_, err := FriendCollection.UpdateOne(ctx, filter, update, options.Update().SetUpsert(true))
	return err
}

func AreFriends(ownerKey, friendName string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"owner_key": ownerKey, "friend_name": friendName}
	var rel FriendRelation
	if err := FriendCollection.FindOne(ctx, filter).Decode(&rel); err != nil {
		return false, ""
	}
	return true, rel.FriendKey
}

func GetFriendNames(ownerKey string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cursor, err := FriendCollection.Find(ctx, bson.M{"owner_key": ownerKey})
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var rels []FriendRelation
	if err := cursor.All(ctx, &rels); err != nil {
		return nil
	}
	res := make([]string, 0, len(rels))
	for _, r := range rels {
		res = append(res, r.FriendName)
	}
	return res
}

func SaveFriendChatLog(conversationID string, seq int64, sender, receiver, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logEntry := FriendChatLog{
		ConversationID: conversationID,
		Seq:            seq,
		Sender:         sender,
		Receiver:       receiver,
		Message:        msg,
		Timestamp:      time.Now().Unix(),
	}
	_, err := FriendChatCollection.InsertOne(ctx, logEntry)
	return err
}

func GetFriendChatLogsSince(conversationID string, afterSeq int64, limit int64) []FriendChatLog {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{
		"conversation_id": conversationID,
		"seq":             bson.M{"$gt": afterSeq},
	}
	opts := options.Find().SetSort(bson.D{{Key: "seq", Value: 1}}).SetLimit(limit)
	cursor, err := FriendChatCollection.Find(ctx, filter, opts)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var logs []FriendChatLog
	if err := cursor.All(ctx, &logs); err != nil {
		return nil
	}
	return logs
}

func EnsureWalletAccount(userKey, userName string, initialBalance int64) (*WalletAccount, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"user_key": userKey}
	update := bson.M{
		"$setOnInsert": bson.M{
			"user_key":   userKey,
			"user_name":  userName,
			"balance":    initialBalance,
			"updated_at": time.Now().Unix(),
		},
		"$set": bson.M{
			"user_name":  userName,
			"updated_at": time.Now().Unix(),
		},
	}
	opts := options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)

	var acc WalletAccount
	if err := WalletCollection.FindOneAndUpdate(ctx, filter, update, opts).Decode(&acc); err != nil {
		return nil, err
	}
	return &acc, nil
}

func GetWalletBalance(userKey string) (int64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var acc WalletAccount
	if err := WalletCollection.FindOne(ctx, bson.M{"user_key": userKey}).Decode(&acc); err != nil {
		return 0, err
	}
	return acc.Balance, nil
}

func FindWalletLedgerByReqID(userKey, reqID string) (*WalletLedger, bool, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var ledger WalletLedger
	err := WalletLedgerCollection.FindOne(ctx, bson.M{"user_key": userKey, "req_id": reqID}).Decode(&ledger)
	if err == mongo.ErrNoDocuments {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &ledger, true, nil
}

// PurchaseItemAWithLedger 演示商品 item_a 扣款（gRPC PurchaseItemA）。
func PurchaseItemAWithLedger(userKey, userName, reqID string, cost int64) (int64, bool, error) {
	return PurchaseWithLedger(userKey, userName, reqID, "item_a", cost)
}

// PurchaseWithLedger 通用扣款账本（商城按 item_id 记账）。
func PurchaseWithLedger(userKey, userName, reqID, itemID string, cost int64) (int64, bool, error) {
	// 幂等：若同 req_id 已成功扣费，直接返回首次结果
	if reqID == "" {
		return 0, false, fmt.Errorf("req_id 不能为空")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	client := WalletLedgerCollection.Database().Client()
	session, err := client.StartSession()
	if err != nil {
		return 0, false, err
	}
	defer session.EndSession(context.Background())

	var insufficientBalance int64

	type txResult struct {
		duplicate      bool
		balanceAfter   int64
		ledgerInserted bool
	}

	txAny, txErr := session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		createdAt := time.Now().Unix()

		// 事务链：先用 wallet_ledger(user_key, req_id) 的唯一约束“锁定幂等键”
		// 只有 insert 成功的那一笔，才继续扣费；重复请求会在这里命中 duplicate key，
		// 直接返回 duplicate，不会发生扣费。
		skeleton := WalletLedger{
			UserKey:       userKey,
			UserName:      userName,
			ReqID:         reqID,
			ItemID:        itemID,
			Cost:          cost,
			BalanceBefore: 0,
			BalanceAfter:  0,
			CreatedAt:     createdAt,
		}

		insRes, insErr := WalletLedgerCollection.InsertOne(sc, skeleton)
		if insErr != nil {
			// 重复 req_id：不扣费，等待事务结束后外层再读首次结果
			if mongo.IsDuplicateKeyError(insErr) {
				return txResult{duplicate: true}, nil
			}
			return nil, insErr
		}

		insertedID, ok := insRes.InsertedID.(primitive.ObjectID)
		if !ok {
			return nil, fmt.Errorf("unexpected insertedID type: %T", insRes.InsertedID)
		}

		// 读取并扣费（FindOneAndUpdate 带 $gte 保证同一用户扣费在原子层面成立）
		var before WalletAccount
		if err := WalletCollection.FindOne(sc, bson.M{"user_key": userKey}).Decode(&before); err != nil {
			return nil, err
		}
		if before.Balance < cost {
			insufficientBalance = before.Balance
			return nil, fmt.Errorf("insufficient balance")
		}

		filter := bson.M{"user_key": userKey, "balance": bson.M{"$gte": cost}}
		update := bson.M{
			"$inc": bson.M{"balance": -cost},
			"$set": bson.M{"updated_at": time.Now().Unix()},
		}
		opts := options.FindOneAndUpdate().SetReturnDocument(options.After)

		var after WalletAccount
		if err := WalletCollection.FindOneAndUpdate(sc, filter, update, opts).Decode(&after); err != nil {
			// 当 $gte 不满足时，可能出现 ErrNoDocuments，这里统一按余额不足处理
			insufficientBalance = before.Balance
			return nil, fmt.Errorf("insufficient balance")
		}

		// 补全账本：把“扣费前后余额”写入 ledger
		updateLedger := bson.M{
			"$set": bson.M{
				"balance_before": before.Balance,
				"balance_after":  after.Balance,
			},
		}
		if _, err := WalletLedgerCollection.UpdateOne(sc, bson.M{"_id": insertedID}, updateLedger); err != nil {
			return nil, err
		}

		return txResult{
			duplicate:    false,
			balanceAfter: after.Balance,
		}, nil
	})

	// 事务层失败：可能是余额不足，也可能是 replica set/事务不支持等
	if txErr != nil {
		if txErr.Error() == "insufficient balance" {
			return insufficientBalance, false, txErr
		}
		// 如果事务失败是因为幂等唯一约束，也按首次结果返回
		if mongo.IsDuplicateKeyError(txErr) {
			// 注意：另一个事务可能尚未提交，所以这里做一点点重试，避免偶发 ErrNoDocuments
			for i := 0; i < 3; i++ {
				var existing WalletLedger
				findCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
				err := WalletLedgerCollection.FindOne(findCtx, bson.M{"user_key": userKey, "req_id": reqID}).Decode(&existing)
				c()
				if err == nil {
					return existing.BalanceAfter, true, nil
				}
				if err == mongo.ErrNoDocuments {
					time.Sleep(time.Duration(30*(i+1)) * time.Millisecond)
					continue
				}
				return 0, true, txErr
			}
			return 0, true, txErr
		}
		return 0, false, txErr
	}

	res, ok := txAny.(txResult)
	if !ok {
		return 0, false, fmt.Errorf("unexpected tx result type: %T", txAny)
	}

	// 重复请求：事务回滚/提交时没有扣费；外层读取首次账本余额
	if res.duplicate {
		// 注意：另一个事务可能尚未提交，所以这里做一点点重试，避免偶发 ErrNoDocuments
		for i := 0; i < 3; i++ {
			var existing WalletLedger
			findCtx, c := context.WithTimeout(context.Background(), 5*time.Second)
			err := WalletLedgerCollection.FindOne(findCtx, bson.M{"user_key": userKey, "req_id": reqID}).Decode(&existing)
			c()
			if err == nil {
				return existing.BalanceAfter, true, nil
			}
			if err == mongo.ErrNoDocuments {
				time.Sleep(time.Duration(30*(i+1)) * time.Millisecond)
				continue
			}
			return 0, true, err
		}
		return 0, true, mongo.ErrNoDocuments
	}

	return res.balanceAfter, false, nil
}

// GrantKillRewardWithLedger 击杀奖励：Mongo 事务内占位 ledger + 对 wallet_accounts 做 balance $inc（加币），与 PurchaseItemAWithLedger 同形。
//
//	幂等: wallet_ledger 唯一 (user_key, req_id)；账本 item_id 形如 kill_reward:{monster_id}；Cost 存 -reward 表示收入。
//	调用方: internal/grpc/wallet_grpc_server.go GrantKillReward（勿从 TCP 层直接调）。
func GrantKillRewardWithLedger(userKey, userName, reqID, monsterID string, reward int64) (int64, bool, error) {
	if reqID == "" {
		return 0, false, fmt.Errorf("req_id 不能为空")
	}
	if reward <= 0 {
		return 0, false, fmt.Errorf("reward 必须大于 0")
	}

	// 命中幂等直接返回首次结果
	if existing, ok, err := FindWalletLedgerByReqID(userKey, reqID); err == nil && ok {
		return existing.BalanceAfter, true, nil
	} else if err != nil {
		return 0, false, err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	// 先保证账户存在
	if _, err := EnsureWalletAccount(userKey, userName, 1000); err != nil {
		return 0, false, err
	}

	client := WalletLedgerCollection.Database().Client()
	session, err := client.StartSession()
	if err != nil {
		return 0, false, err
	}
	defer session.EndSession(context.Background())

	type txResult struct {
		duplicate    bool
		balanceAfter int64
	}

	txAny, txErr := session.WithTransaction(ctx, func(sc mongo.SessionContext) (interface{}, error) {
		createdAt := time.Now().Unix()
		// 占位流水，唯一约束承担幂等锁
		skeleton := WalletLedger{
			UserKey:       userKey,
			UserName:      userName,
			ReqID:         reqID,
			ItemID:        "kill_reward:" + monsterID,
			Cost:          -reward, // 正向奖励，用负 cost 表示“收入”。
			BalanceBefore: 0,
			BalanceAfter:  0,
			CreatedAt:     createdAt,
		}
		insRes, insErr := WalletLedgerCollection.InsertOne(sc, skeleton)
		if insErr != nil {
			if mongo.IsDuplicateKeyError(insErr) {
				return txResult{duplicate: true}, nil
			}
			return nil, insErr
		}
		insertedID, ok := insRes.InsertedID.(primitive.ObjectID)
		if !ok {
			return nil, fmt.Errorf("unexpected insertedID type: %T", insRes.InsertedID)
		}

		var before WalletAccount
		if err := WalletCollection.FindOne(sc, bson.M{"user_key": userKey}).Decode(&before); err != nil {
			return nil, err
		}

		update := bson.M{
			"$inc": bson.M{"balance": reward},
			"$set": bson.M{"updated_at": time.Now().Unix()},
		}
		opts := options.FindOneAndUpdate().SetReturnDocument(options.After)
		var after WalletAccount
		if err := WalletCollection.FindOneAndUpdate(sc, bson.M{"user_key": userKey}, update, opts).Decode(&after); err != nil {
			return nil, err
		}

		_, err = WalletLedgerCollection.UpdateOne(sc, bson.M{"_id": insertedID}, bson.M{
			"$set": bson.M{
				"balance_before": before.Balance,
				"balance_after":  after.Balance,
			},
		})
		if err != nil {
			return nil, err
		}
		return txResult{duplicate: false, balanceAfter: after.Balance}, nil
	})
	if txErr != nil {
		if mongo.IsDuplicateKeyError(txErr) {
			if existing, ok, err := FindWalletLedgerByReqID(userKey, reqID); err == nil && ok {
				return existing.BalanceAfter, true, nil
			}
		}
		return 0, false, txErr
	}
	res, ok := txAny.(txResult)
	if !ok {
		return 0, false, fmt.Errorf("unexpected tx result type: %T", txAny)
	}
	if res.duplicate {
		if existing, ok, err := FindWalletLedgerByReqID(userKey, reqID); err == nil && ok {
			return existing.BalanceAfter, true, nil
		}
	}
	return res.balanceAfter, false, nil
}

func GetRecentRoomChatLogs(roomID string, limit int64) []RoomChatLog {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"room_id": roomID}

	opts := options.Find().SetSort(bson.D{{Key: "timestamp", Value: -1}}).SetLimit(limit)

	cursor, err := RoomChatCollection.Find(ctx, filter, opts)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var logs []RoomChatLog
	if err = cursor.All(ctx, &logs); err != nil {
		return nil
	}

	//反转切片获得正确数据
	for i, j := 0, len(logs)-1; i < j; i, j = i+1, j-1 {
		logs[i], logs[j] = logs[j], logs[i]
	}

	return logs
}

//func SavePrivateChatLog(sender, target, msg string) {
//	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
//	defer cancel()
//
//	// 生成一个唯一的会话ID，无论 A发给B 还是 B发给A，SessionID 都一样
//	// 简单算法：按字典序排序两个名字，中间加下划线
//	var sessionID string
//	if sender < target {
//		sessionID = "PM_" + sender + "_" + target
//	} else {
//		sessionID = "PM_" + target + "_" + sender
//	}
//
//	logEntry := ChatLog{
//		SessionID: sessionID,
//		Sender:    sender,
//		Message:   msg,
//		Timestamp: time.Now().Unix(),
//	}
//
//	_, err := ChatCollection.InsertOne(ctx, logEntry)
//	if err != nil {
//		fmt.Println("保存私聊记录失败:", err)
//	} else {
//		fmt.Printf("私聊记录已保存: %s -> %s\n", sender, target)
//	}
//}
