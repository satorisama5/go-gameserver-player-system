// db.go
package internal

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

var DB *mongo.Database
var UserCollection *mongo.Collection
var BlacklistCollection *mongo.Collection
var ChatCollection *mongo.Collection

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
	UniqueKey string  `bson:"unique_key"`
	Username  string  `bson:"username"`
	LastIp    string  `bson:"last_ip"`
	PositionX float32 `bson:"pos_x"`
	PositionY float32 `bson:"pos_y"`
	PositionZ float32 `bson:"pos_z"`
	UpdatedAt int64   `bson:"updated_at"`
	LastScene string  `bson:"last_scene"`
}

type ChatLog struct {
	SessionID string `bson:"session_id"`
	Sender    string `bson:"sender"`
	Message   string `bson:"message"` // 存过滤后的内容
	Timestamp int64  `bson:"timestamp"`
}

func InitDB() {
	// 设置连接超时
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	mongoURI := Conf.Database.MongoURI
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
	ChatCollection = DB.Collection("chat_logs")
}

// 辅助函数：保存/更新玩家数据
func SavePlayerToDB(uniqueKey string, name string, pos PlayerPosition, roomName string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"unique_key": uniqueKey}
	update := bson.M{
		"$set": bson.M{
			"username":   name,
			"pos_x":      pos.X,
			"pos_y":      pos.Y,
			"pos_z":      pos.Z,
			"last_scene": roomName,
			"updated_at": time.Now().Unix(),
		},
	}
	// Upsert: true 表示如果不存在则插入，存在则更新
	opts := options.Update().SetUpsert(true)

	_, err := UserCollection.UpdateOne(ctx, filter, update, opts)
	if err != nil {
		fmt.Println("保存玩家数据失败:", err)
	}
}

func LoadPlayerFromDB(uniqueKey string) (*PlayerModel, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var result PlayerModel
	filter := bson.M{"unique_key": uniqueKey}
	err := UserCollection.FindOne(ctx, filter).Decode(&result)
	if err != nil {
		return nil, false
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

func SaveChatLog(sessionID, sender, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	logEntry := ChatLog{
		SessionID: sessionID,
		Sender:    sender,
		Message:   msg,
		Timestamp: time.Now().Unix(),
	}

	_, err := ChatCollection.InsertOne(ctx, logEntry)
	if err != nil {
		fmt.Println("保存聊天记录失败:", err)
	}
}

func GetRecentChatLogs(sessionID string, limit int64) []ChatLog {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	filter := bson.M{"session_id": sessionID}

	opts := options.Find().SetSort(bson.D{{"timestamp", -1}}).SetLimit(limit)

	cursor, err := ChatCollection.Find(ctx, filter, opts)
	if err != nil {
		return nil
	}
	defer cursor.Close(ctx)

	var logs []ChatLog
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
