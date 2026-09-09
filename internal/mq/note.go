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

const defaultNoteQueueName = "room_note_snapshots"

func noteQueueName() string {
	if cfg.Conf.MQ.NoteQueueName != "" {
		return cfg.Conf.MQ.NoteQueueName
	}
	return defaultNoteQueueName
}

// declareNoteQueue 在 InitMQ 已建连后声明笔记队列（不影响聊天队列）。
func declareNoteQueue() {
	if MQChannel == nil {
		return
	}
	q := noteQueueName()
	_, err := MQChannel.QueueDeclare(q, true, false, false, false, nil)
	if err != nil {
		log.Printf("声明房间笔记队列失败(笔记持久化将不可用): %v", err)
		return
	}
	fmt.Printf("RabbitMQ 笔记队列已就绪（note=%s）\n", q)
}

// PublishRoomNoteSnapshotToMQ 将笔记快照投递到独立队列，异步写 Mongo。
func PublishRoomNoteSnapshotToMQ(doc storage.RoomNoteDoc) {
	if MQChannel == nil {
		return
	}
	if doc.UpdatedAt == 0 {
		doc.UpdatedAt = time.Now().Unix()
	}
	body, err := json.Marshal(doc)
	if err != nil {
		log.Printf("笔记快照 JSON 失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.note.marshal_failed",
			Level:     "error",
			Message:   "marshal room note snapshot failed",
		})
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	q := noteQueueName()
	err = MQChannel.PublishWithContext(ctx, "", q, false, false, amqp.Publishing{
		DeliveryMode: amqp.Persistent,
		ContentType:  "application/json",
		Body:         body,
	})
	if err != nil {
		log.Printf("笔记快照 MQ 发送失败: %v", err)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType: "mq.publish.note.failed",
			Level:     "error",
			Message:   "publish room note snapshot failed",
			Meta: bson.M{
				"room_session_id": doc.RoomSessionID,
				"note_id":         doc.NoteID,
				"error":           err.Error(),
			},
		})
	}
}

// RunNoteSnapshotConsumer 独立消费者：写 room_notes。与聊天消费者分离，互不影响。
func RunNoteSnapshotConsumer() {
	if MQChannel == nil {
		return
	}
	q := noteQueueName()
	msgs, err := MQChannel.Consume(q, "", false, false, false, false, nil)
	if err != nil {
		log.Printf("无法注册笔记快照消费者: %v", err)
		return
	}
	fmt.Printf("正在监听 MQ 笔记快照队列（note=%s）...\n", q)
	go func() {
		for d := range msgs {
			var doc storage.RoomNoteDoc
			if err := json.Unmarshal(d.Body, &doc); err != nil {
				log.Printf("笔记快照解析失败: %v", err)
				_ = d.Ack(false)
				continue
			}
			if err := storage.UpsertRoomNoteSnapshot(doc); err != nil {
				log.Printf("笔记快照写库失败，稍后重试: %v", err)
				_ = d.Nack(false, true)
				continue
			}
			_ = d.Ack(false)
		}
	}()
}
