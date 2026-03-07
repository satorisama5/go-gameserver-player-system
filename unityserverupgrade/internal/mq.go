// internal/mq.go
package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
)

// 全局 MQ 信道变量
var MQChannel *amqp.Channel

// InitMQ 初始化 RabbitMQ 连接并声明队列
func InitMQ() {
	// 1. 连接 RabbitMQ (地址从 config.yaml 读取)
	conn, err := amqp.Dial(Conf.MQ.Url)
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

	// 3. 声明队列 (如果不存在则创建，durable=true 表示 MQ 重启后队列依然存在)
	_, err = ch.QueueDeclare(
		Conf.MQ.QueueName, // 队列名，如 "chat_logs"
		true,              // 持久化 (durable)
		false,             // 自动删除
		false,             // 排他性
		false,             // no-wait
		nil,               // 参数
	)
	if err != nil {
		log.Fatalf("无法声明队列: %v", err)
	}

	fmt.Println("RabbitMQ 连接成功，队列已就绪！")
}

// PublishChatLogToMQ 【生产者】：将聊天记录投递到 MQ
func PublishChatLogToMQ(sessionID, sender, msg string) {
	// 直接复用 db.go 里的 ChatLog 结构体，保证数据字段一致
	logMsg := ChatLog{
		SessionID: sessionID,
		Sender:    sender,
		Message:   msg,
		Timestamp: time.Now().Unix(),
	}

	body, err := json.Marshal(logMsg)
	if err != nil {
		log.Printf("解析 JSON 失败: %v", err)
		return
	}

	// 设置 5 秒发送超时 context
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 投递消息
	err = MQChannel.PublishWithContext(ctx,
		"",                // exchange (使用默认交换机)
		Conf.MQ.QueueName, // routing key (必须与队列名一致)
		false,             // mandatory
		false,             // immediate
		amqp.Publishing{
			DeliveryMode: amqp.Persistent, // 消息持久化，防止 MQ 宕机丢失未处理的消息
			ContentType:  "application/json",
			Body:         body,
		})

	if err != nil {
		log.Printf("MQ 发送失败: %v", err)
	}
}

// StartChatConsumer 【消费者】：启动后台协程，从 MQ 搬运数据到 MongoDB
func StartChatConsumer() {
	// 注册消费者
	msgs, err := MQChannel.Consume(
		Conf.MQ.QueueName,
		"",   // 消费者标签
		true, // auto-ack: 自动确认消息已收到
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		log.Printf("无法注册消费者: %v", err)
		return
	}

	fmt.Println("正在监听 MQ 聊天日志队列，准备写入数据库...")

	// 启动一个独立的协程监听消息
	go func() {
		for d := range msgs {
			// 1. 解析从 MQ 拿到的字节流
			var logEntry ChatLog
			if err := json.Unmarshal(d.Body, &logEntry); err != nil {
				log.Printf("MQ 消息解析失败: %v", err)
				continue
			}

			// 2. 【核心复用】：直接调用 db.go 中成熟的存库函数
			// 这样做可以确保所有的数据库写入逻辑（包括 SessionID 生成、InsertOne 等）都统一管理
			SaveChatLog(logEntry.SessionID, logEntry.Sender, logEntry.Message)
		}
	}()
}
