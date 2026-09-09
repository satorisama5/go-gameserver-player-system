// internal/mq.go
package mq

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.mongodb.org/mongo-driver/bson"

	cfg "unityserverupgrade/internal/config"
	storage "unityserverupgrade/internal/storage"
)

// 全局 MQ 信道变量
var MQChannel *amqp.Channel

// InitMQ 初始化 RabbitMQ 连接并声明队列
func InitMQ() {
	// 1. 连接 RabbitMQ (地址从 config.yaml 读取)
	conn, err := amqp.Dial(cfg.Conf.MQ.Url)
	if err != nil {
		log.Fatalf("无法连接到 RabbitMQ: %v", err)
	}
	// 注意：在大型项目中，建议在这里保存 conn 并在 main 退出时关闭，这里为了演示保持简洁

	// 2. 打开通道 (Channel)
	ch, err := conn.Channel()
	if err != nil {
		log.Fatalf("无法打开 MQ Channel: %v", err)
	}
	MQChannel = ch

	roomQueue := cfg.Conf.MQ.RoomQueueName
	if roomQueue == "" {
		roomQueue = "room_chat_logs"
	}
	privateQueue := cfg.Conf.MQ.PrivateQueueName
	if privateQueue == "" {
		privateQueue = "private_chat_logs"
	}

	// 3. 声明房间聊天队列
	_, err = ch.QueueDeclare(
		roomQueue,
		true,              // 持久化 (durable)
		false,             // 自动删除
		false,             // 排他性
		false,             // no-wait
		nil,               // 参数
	)
	if err != nil {
		log.Fatalf("无法声明房间聊天队列: %v", err)
	}

	// 4. 声明私聊队列
	_, err = ch.QueueDeclare(
		privateQueue,
		true,  // 持久化 (durable)
		false, // 自动删除
		false, // 排他性
		false, // no-wait
		nil,   // 参数
	)
	if err != nil {
		log.Fatalf("无法声明私聊队列: %v", err)
	}

	fmt.Printf("RabbitMQ 连接成功，队列已就绪（room=%s, private=%s）\n", roomQueue, privateQueue)
	declareNoteQueue() // 独立笔记队列；失败不影响聊天队列
}

// PublishRoomChatLogToMQ 将房间聊天记录投递到房间队列
func PublishRoomChatLogToMQ(roomID, sender, msg string) {
	logMsg := storage.RoomChatLog{
		RoomID:    roomID,
		Sender:    sender,
		Message:   msg,
		Timestamp: time.Now().Unix(),
	}

	body, err := json.Marshal(logMsg)
	if err != nil {
		log.Printf("解析 JSON 失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.room.marshal_failed",
			Level:     "error",
			Message:   "marshal room chat log failed",
		})
		return
	}

	// 设置 5 秒发送超时 context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queueName := cfg.Conf.MQ.RoomQueueName
	if queueName == "" {
		queueName = "room_chat_logs"
	}

	err = MQChannel.PublishWithContext(ctx,
		"",                // exchange (使用默认交换机)
		queueName,         // routing key (必须与队列名一致)
		false,             // mandatory
		false,             // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent, // 消息持久化，防止 MQ 宕机丢失未处理的消息
			ContentType:  "application/json",
			Body:         body,
		})

	if err != nil {
		log.Printf("房间聊天 MQ 发送失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.room.failed",
			Level:     "error",
			Message:   "publish room chat log failed",
			Meta: bson.M{
				"room_id": roomID,
				"sender":  sender,
				"error":   err.Error(),
			},
		})
	}
}

// PublishPrivateChatLogToMQ 将私聊记录投递到私聊队列
func PublishPrivateChatLogToMQ(conversationID, sender, receiver, msg string) {
	logMsg := storage.PrivateChatLog{
		ConversationID: conversationID,
		Sender:         sender,
		Receiver:       receiver,
		Message:        msg,
		Timestamp:      time.Now().Unix(),
	}

	body, err := json.Marshal(logMsg)
	if err != nil {
		log.Printf("解析 JSON 失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.private.marshal_failed",
			Level:     "error",
			Message:   "marshal private chat log failed",
		})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	queueName := cfg.Conf.MQ.PrivateQueueName
	if queueName == "" {
		queueName = "private_chat_logs"
	}

	err = MQChannel.PublishWithContext(ctx,
		"",
		queueName,
		false,
		false,
		amqp.Publishing{
			DeliveryMode: amqp.Persistent,
			ContentType:  "application/json",
			Body:         body,
		})

	if err != nil {
		log.Printf("私聊 MQ 发送失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.private.failed",
			Level:     "error",
			Message:   "publish private chat log failed",
			Meta: bson.M{
				"conversation_id": conversationID,
				"sender":          sender,
				"receiver":        receiver,
				"error":           err.Error(),
			},
		})
	}
}
