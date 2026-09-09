package storage

import (
	"encoding/json"
	"time"

	protocol "unityserverupgrade/internal/protocol"
)

const roomSnapTTL = 7 * 24 * time.Hour

// MonsterSnap 房间快照中的怪物。
type MonsterSnap struct {
	ID         string                   `json:"id"`
	TemplateID string                   `json:"template_id"`
	Name       string                   `json:"name"`
	HP         int64                    `json:"hp"`
	MaxHP      int64                    `json:"max_hp"`
	Position   protocol.PlayerPosition  `json:"pos"`
	ScoreDelta int32                    `json:"score_delta"`
	GoldReward int64                    `json:"gold_reward"`
}

// RoomSnapshot Redis 持久化的房间壳（不含在线连接；进程重启后可重建房间+怪）。
type RoomSnapshot struct {
	Name       string        `json:"name"`
	SessionID  string        `json:"session_id"`
	MaxPlayers int           `json:"max_players"`
	Password   string        `json:"password"`
	Kind       string        `json:"kind,omitempty"` // "" | pvp_ladder
	Monsters   []MonsterSnap `json:"monsters"`
	UpdatedAt  int64         `json:"updated_at"`
}

func roomSnapKey(roomName string) string {
	return "room:snap:" + roomName
}

// SaveRoomSnapshot 覆盖写入房间快照。
func SaveRoomSnapshot(snap RoomSnapshot) {
	if RDB == nil || snap.Name == "" {
		return
	}
	snap.UpdatedAt = time.Now().Unix()
	body, err := json.Marshal(snap)
	if err != nil {
		return
	}
	_ = RDB.Set(Ctx, roomSnapKey(snap.Name), body, roomSnapTTL).Err()
}

// LoadRoomSnapshot 读取房间快照。
func LoadRoomSnapshot(roomName string) *RoomSnapshot {
	if RDB == nil || roomName == "" {
		return nil
	}
	s, err := RDB.Get(Ctx, roomSnapKey(roomName)).Result()
	if err != nil {
		return nil
	}
	var snap RoomSnapshot
	if json.Unmarshal([]byte(s), &snap) != nil {
		return nil
	}
	return &snap
}

// DeleteRoomSnapshot 删快照（可选；空房也可保留以便恢复）。
func DeleteRoomSnapshot(roomName string) {
	if RDB == nil || roomName == "" {
		return
	}
	_ = RDB.Del(Ctx, roomSnapKey(roomName)).Err()
}
