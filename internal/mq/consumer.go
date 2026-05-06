// consumer.go 独立消费 RabbitMQ 聊天日志队列，写入 MongoDB，实现异步解耦与削峰。
package mq

import (
	"encoding/json"
	"fmt"
	"log"

	"go.mongodb.org/mongo-driver/bson"
	cfg "unityserverupgrade/internal/config"
	storage "unityserverupgrade/internal/storage"
)

// RunChatLogConsumer 启动消费者：从 MQ 拉取消息并写入 MongoDB。
// 需在 InitMQ() 之后调用；在 gamed 中与主服务同进程运行，也可单独起进程只跑此消费者。
func RunChatLogConsumer() {
	roomQueue := cfg.Conf.MQ.RoomQueueName
	if roomQueue == "" {
		roomQueue = "room_chat_logs"
	}
	privateQueue := cfg.Conf.MQ.PrivateQueueName
	if privateQueue == "" {
		privateQueue = "private_chat_logs"
	}

	roomMsgs, err := MQChannel.Consume(
		roomQueue,
		"",   // 消费者标签
		false, // auto-ack=false，改为手动 ack
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("无法注册房间聊天消费者: %v", err)
		return
	}
	privateMsgs, err := MQChannel.Consume(
		privateQueue,
		"",
		false,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("无法注册私聊消费者: %v", err)
		return
	}

	fmt.Printf("正在监听 MQ 聊天日志队列（room=%s, private=%s）...\n", roomQueue, privateQueue)

	go func() {
		for d := range roomMsgs {
			var logEntry storage.RoomChatLog
			if err := json.Unmarshal(d.Body, &logEntry); err != nil {
				log.Printf("房间消息解析失败: %v", err)
				storage.SaveEventLogAsync(storage.EventLog{
					EventType: "mq.consume.room.unmarshal_failed",
					Level:     "warn",
					Message:   "room message unmarshal failed, dropped",
				})
				_ = d.Ack(false) // 畸形消息直接丢弃
				continue
			}
			if err := storage.SaveRoomChatLog(logEntry.RoomID, logEntry.Sender, logEntry.Message); err != nil {
				log.Printf("房间消息写库失败，稍后重试: %v", err)
				storage.SaveEventLogAsync(storage.EventLog{
					EventType: "mq.consume.room.persist_failed",
					Level:     "error",
					Message:   "room message persist failed, requeue",
					Meta: bson.M{
						"room_id": logEntry.RoomID,
						"sender":  logEntry.Sender,
						"error":   err.Error(),
					},
				})
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}()

	go func() {
		for d := range privateMsgs {
			var logEntry storage.PrivateChatLog
			if err := json.Unmarshal(d.Body, &logEntry); err != nil {
				log.Printf("私聊消息解析失败: %v", err)
				storage.SaveEventLogAsync(storage.EventLog{
					EventType: "mq.consume.private.unmarshal_failed",
					Level:     "warn",
					Message:   "private message unmarshal failed, dropped",
				})
				_ = d.Ack(false) // 畸形消息直接丢弃
				continue
			}
			if err := storage.SavePrivateChatLog(logEntry.ConversationID, logEntry.Sender, logEntry.Receiver, logEntry.Message); err != nil {
				log.Printf("私聊消息写库失败，稍后重试: %v", err)
				storage.SaveEventLogAsync(storage.EventLog{
					EventType: "mq.consume.private.persist_failed",
					Level:     "error",
					Message:   "private message persist failed, requeue",
					Meta: bson.M{
						"conversation_id": logEntry.ConversationID,
						"sender":          logEntry.Sender,
						"receiver":        logEntry.Receiver,
						"error":           err.Error(),
					},
				})
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}()
}
