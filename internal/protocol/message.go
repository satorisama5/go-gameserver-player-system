package protocol

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
	ID        int             `json:"id"`
	Data      json.RawMessage `json:"data"` // 使用 RawMessage 延迟解析，非常高效
	SessionID string `json:"session_id,omitempty"` // 会话标识（房间/私聊会话）；可选，omitempty 表示可省略
	ReqID     string `json:"req_id,omitempty"`     // 请求唯一ID（幂等/查重/分辨请求）
	Seq       int64  `json:"seq,omitempty"`        // 消息序号（断线重连游标）
	Ts        int64  `json:"ts,omitempty"`         // 时间戳（毫秒）
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
	MSG_ID_PLAYER_MOVE_REQ = 2002 // C->S 玩家移动上报（角色行为大类-1）

	// 角色行为大类-2：战斗与时间轮相关（原 command 文本 bcast/addbuff/... 迁移至此）
	MSG_ID_PLAYER_BATTLE = 2003 // C->S 战斗动作：data 为 PlayerBattlePayload（JSON）

	// 角色行为大类-3：击杀奖励链路（原 killreward 命令迁移至此）
	MSG_ID_PLAYER_REWARD = 2004 // C->S 击杀奖励：data 为 PlayerKillRewardPayload（JSON）
)

// PlayerBattlePayload 与 MSG_ID_PLAYER_BATTLE 配套。
// op: cast | addbuff | buffs | bstate
type PlayerBattlePayload struct {
	Op         string `json:"op"`
	Seq        int64  `json:"seq,omitempty"`
	Skill      string `json:"skill,omitempty"`
	Target     string `json:"target,omitempty"`
	Buff       string `json:"buff,omitempty"`
	DurationMs int64  `json:"duration_ms,omitempty"`
}

// PlayerKillRewardPayload 与 MSG_ID_PLAYER_REWARD 配套；幂等键使用外层 Message.req_id，缺省时服务端用 kr_{kill_id}。
type PlayerKillRewardPayload struct {
	KillID     string `json:"kill_id"`
	MonsterID  string `json:"monster_id"`
	ScoreDelta int32  `json:"score_delta"`
	GoldReward int64  `json:"gold_reward"`
}

// PackTextMessage 打包文本消息：S->C 普通文本（兼容现有客户端）。
func PackTextMessage(msg string) []byte {
	msgData, _ := json.Marshal(msg)
	jsonMsg, _ := json.Marshal(Message{ID: MSG_ID_TEXT_MESSAGE, Data: msgData})
	return jsonMsg
}

// PackTextMessageWithReqID 打包文本消息并附带 req_id，供请求-响应关联。
func PackTextMessageWithReqID(msg, reqID string) []byte {
	msgData, _ := json.Marshal(msg)
	jsonMsg, _ := json.Marshal(Message{
		ID:    MSG_ID_TEXT_MESSAGE,
		Data:  msgData,
		ReqID: reqID,
	})
	return jsonMsg
}
