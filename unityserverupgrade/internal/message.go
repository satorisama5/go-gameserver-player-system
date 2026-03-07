package internal

import "encoding/json"

// C -> S: 客户端上报自己的位置
// 后面的表示会被json解析为怎样的字符
type PlayerPosition struct {
	X float32 `json:"x"`
	Y float32 `json:"y"`
	Z float32 `json:"z"`
}

// S -> C: 服务器广播的单个玩家状态
type PlayerState struct {
	Name     string         `json:"name"`
	Position PlayerPosition `json:"pos"`
}

// S -> C: 服务器广播的完整场景状态
type SceneStateBroadcast struct {
	Players map[string]PlayerState `json:"players"`
}

// 通用消息结构，用于识别消息类型
type Message struct {
	ID   int             `json:"id"`
	Data json.RawMessage `json:"data"` // 使用 RawMessage 延迟解析，非常高效
}

// C# 客户端会将 CommandMessage 对象序列化成 {"cmd":"rename|张三"}
// 这个结构体就是用来解析这个 JSON 的
type CommandMessage struct {
	Cmd string `json:"cmd"`
} //通常，当你用 json.Unmarshal 解析一个 JSON 字符串到一个结构体时，它会把 JSON 的所有部分都解析并填充到对应的字段里

// 定义消息ID常量，必须与客户端完全一致
// 这部分代码使用 const 关键字定义了一组常量，它们是客户端和服务器之间预先约定好的“协议号”或“消息类型ID”。
const (
	MSG_ID_TEXT_MESSAGE = 0    // S->C 普通文本消息
	MSG_ID_COMMAND      = 1001 // C->S 客户端发来的文本指令

	MSG_ID_HEARTBEAT = 1002 //

	MSG_ID_SCENE_STATE     = 2001 // S->C 场景状态广播
	MSG_ID_PLAYER_MOVE_REQ = 2002 // C->S 玩家移动上报
)
