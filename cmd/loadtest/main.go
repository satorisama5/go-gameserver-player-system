// 连接数压测小工具：按 [2 字节长度头 + JSON 体] 协议建 N 个长连接；支持仅心跳或真实行为模拟。
// 使用：go run ./cmd/loadtest -addr 127.0.0.1:8888 -n 500 -interval 5s
// 真实模式：go run ./cmd/loadtest -addr 127.0.0.1:8888 -mode real -n 500 -duration 60s
package main

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// 与 internal/message.go 保持一致
const (
	MSG_ID_COMMAND         = 1001
	MSG_ID_HEARTBEAT       = 1002
	MSG_ID_PLAYER_MOVE_REQ = 2002
)

type Message struct {
	ID   int             `json:"id"`
	Data json.RawMessage `json:"data"`
}

type CommandMessage struct {
	Cmd string `json:"cmd"`
}

type PlayerPosition struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

// packPacket 打包为 [2 字节大端长度 + JSON 体]
func packPacket(msg Message) []byte {
	body, _ := json.Marshal(msg)
	p := make([]byte, 2+len(body))
	binary.BigEndian.PutUint16(p[0:2], uint16(len(body)))
	copy(p[2:], body)
	return p
}

var (
	statsTotalSent int64
	statsTotalFail int64
	statsStart     time.Time
)

func main() {
	addr := flag.String("addr", "127.0.0.1:8888", "服务器地址，例如 127.0.0.1:8888") //addr定义指针，flag用来创建接收参数的变量指针
	n := flag.Int("n", 200, "要建立的连接数")
	mode := flag.String("mode", "heartbeat", "heartbeat=仅心跳; real=模拟真实玩家(rename+移动+聊天+心跳)")
	interval := flag.Duration("interval", 5*time.Second, "每条连接发送心跳的间隔，例如 5s、10s（仅 heartbeat 模式）")
	duration := flag.Duration("duration", 0, "压测持续时间，0 表示只建连并打一次心跳后退出；>0 时持续。real 模式建议 -duration 60s")
	ramp := flag.Duration("ramp", 0, "建连拉平时间：在此时长内均匀发起连接；0=不拉平。建议 -n 较大时用 -ramp 5s 或 -ramp 20s")
	flag.Parse()

	realMode := *mode == "real"
	if realMode && *duration <= 0 {
		*duration = 60 * time.Second
	}

	fmt.Printf("目标: %s, 连接数: %d, 模式: %s", *addr, *n, *mode)
	if *ramp > 0 {
		fmt.Printf(", 建连拉平: %v", *ramp)
	}
	if *duration > 0 {
		fmt.Printf(", 持续: %v", *duration)
	}
	if !realMode {
		fmt.Printf(", 心跳间隔: %v", *interval)
	}
	fmt.Println()
	fmt.Println("---")

	statsStart = time.Now()
	var alive int32
	var dialFail int32
	var wg sync.WaitGroup

	heartbeatPacket := packPacket(Message{ID: MSG_ID_HEARTBEAT, Data: json.RawMessage("null")})

	rampStart := time.Now()
	for i := 0; i < *n; i++ {
		wg.Add(1)
		if *ramp > 0 && *n > 1 {
			target := time.Duration(int64(*ramp) * int64(i) / int64(*n-1))
			if elapsed := time.Since(rampStart); target > elapsed {
				time.Sleep(target - elapsed)
			}
		}
		if realMode {
			go runRealPlayer(*addr, i, *duration, &alive, &dialFail, &wg)
		} else {
			go runHeartbeat(*addr, i, heartbeatPacket, *interval, *duration, &alive, &dialFail, &wg)
		}
	}

	wg.Wait()
	elapsed := time.Since(statsStart)

	fmt.Println("---")
	fmt.Printf("请求连接数: %d\n", *n)
	fmt.Printf("建连/写失败(含被拒): %d\n", dialFail)
	fmt.Printf("当前存活连接数: %d\n", alive)
	if realMode {
		fmt.Printf("总发包数: %d\n", atomic.LoadInt64(&statsTotalSent))
		fmt.Printf("发包失败数: %d\n", atomic.LoadInt64(&statsTotalFail))
		if elapsed.Seconds() > 0 {
			fmt.Printf("平均每秒发包量: %.1f\n", float64(atomic.LoadInt64(&statsTotalSent))/elapsed.Seconds())
		}
	}
	if *duration > 0 {
		fmt.Printf("说明: 上述「存活」为持续 %v 内未断开的连接数。\n", *duration)
	} else if !realMode {
		fmt.Println("说明: 若需观察「运行一段时间后还剩多少」，请加 -duration 如 -duration 60s。")
	}
}

// runHeartbeat 原有仅心跳模式
func runHeartbeat(addr string, id int, heartbeatPacket []byte, interval, duration time.Duration, alive, dialFail *int32, wg *sync.WaitGroup) {
	defer wg.Done()
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second) //注意这里关键是dial
	if err != nil {
		atomic.AddInt32(dialFail, 1)
		if id < 5 || (id+1)%100 == 0 {
			log.Printf("[%d] Dial 失败: %v", id, err)
		}
		return
	}
	defer conn.Close()

	if _, err := conn.Write(heartbeatPacket); err != nil {
		atomic.AddInt32(dialFail, 1)
		if id < 5 {
			log.Printf("[%d] 首次写入失败: %v", id, err)
		}
		return
	}
	atomic.AddInt32(alive, 1)

	go drainConn(conn)

	if duration <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	end := time.Now().Add(duration)
	writeDeadline := 10 * time.Second
	for time.Now().Before(end) {
		<-ticker.C
		_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))
		if _, err := conn.Write(heartbeatPacket); err != nil {
			atomic.AddInt32(alive, -1)
			if id < 3 {
				log.Printf("[%d] 心跳写入失败: %v", id, err)
			}
			return
		}
	}
}

// runRealPlayer 真实行为模拟：rename -> 每 100ms 移动、每 3s 聊天、每 5s 心跳
func runRealPlayer(addr string, id int, duration time.Duration, alive, dialFail *int32, wg *sync.WaitGroup) {
	defer wg.Done()
	conn, err := net.DialTimeout("tcp", addr, 10*time.Second)
	if err != nil {
		atomic.AddInt32(dialFail, 1)
		if id < 5 || (id+1)%100 == 0 {
			log.Printf("[%d] Dial 失败: %v", id, err)
		}
		return
	}
	defer conn.Close()

	writeDeadline := 10 * time.Second
	send := func(p []byte) bool {
		_ = conn.SetWriteDeadline(time.Now().Add(writeDeadline))
		_, err := conn.Write(p)
		if err != nil {
			atomic.AddInt64(&statsTotalFail, 1)
			return false
		}
		atomic.AddInt64(&statsTotalSent, 1)
		return true
	}

	// 1. 先发 rename：cmd=rename|Bot_<id>|loadtest_<id>
	renameCmd, _ := json.Marshal(CommandMessage{Cmd: fmt.Sprintf("rename|Bot_%d|loadtest_%d", id, id)})
	renamePkt := packPacket(Message{ID: MSG_ID_COMMAND, Data: renameCmd})
	if !send(renamePkt) {
		atomic.AddInt32(dialFail, 1)
		if id < 5 {
			log.Printf("[%d] rename 发送失败", id)
		}
		return
	}
	atomic.AddInt32(alive, 1)

	go drainConn(conn)

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	moveTicker := time.NewTicker(100 * time.Millisecond)
	chatTicker := time.NewTicker(3 * time.Second)
	heartbeatTicker := time.NewTicker(5 * time.Second)
	defer moveTicker.Stop()
	defer chatTicker.Stop()
	defer heartbeatTicker.Stop()

	// 随机移动：在 -200~200 范围内小步移动，避免服务端移速校验踢掉
	rng := rand.New(rand.NewSource(int64(id) + time.Now().UnixNano()))
	pos := PlayerPosition{X: float32(rng.Intn(400) - 200), Y: 99, Z: float32(rng.Intn(400) - 200)}

	for {
		select {
		case <-ctx.Done():
			return
		case <-moveTicker.C:
			pos.X += float32(rng.Intn(31)-15) * 0.1
			pos.Z += float32(rng.Intn(31)-15) * 0.1
			data, _ := json.Marshal(pos)
			pkt := packPacket(Message{ID: MSG_ID_PLAYER_MOVE_REQ, Data: data})
			if !send(pkt) {
				atomic.AddInt32(alive, -1)
				return
			}
		case <-chatTicker.C:
			chatCmd, _ := json.Marshal(CommandMessage{Cmd: "chat|hello"})
			pkt := packPacket(Message{ID: MSG_ID_COMMAND, Data: chatCmd})
			if !send(pkt) {
				atomic.AddInt32(alive, -1)
				return
			}
		case <-heartbeatTicker.C:
			pkt := packPacket(Message{ID: MSG_ID_HEARTBEAT, Data: json.RawMessage("null")})
			if !send(pkt) {
				atomic.AddInt32(alive, -1)
				return
			}
		}
	}
}

func drainConn(conn net.Conn) {
	buf := make([]byte, 4096)
	for {
		_, err := conn.Read(buf)
		if err != nil {
			return
		}
	}
}
