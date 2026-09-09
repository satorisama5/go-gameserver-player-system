package world

import (
	"fmt"

	storage "unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/world/monster"
)

// TryRestoreRoom 若内存无房但 Redis 有快照，则重建房间壳并返回。
func (s *Server) TryRestoreRoom(roomName string) *Room {
	if roomName == "" {
		return nil
	}
	s.RoomLock.Lock()
	defer s.RoomLock.Unlock()
	if r, ok := s.Rooms[roomName]; ok {
		return r
	}
	snap := storage.LoadRoomSnapshot(roomName)
	if snap == nil {
		return nil
	}
	mobs := make([]monster.Monster, 0, len(snap.Monsters))
	for _, ms := range snap.Monsters {
		mobs = append(mobs, monster.Monster{
			ID: ms.ID, TemplateID: ms.TemplateID, Name: ms.Name,
			HP: ms.HP, MaxHP: ms.MaxHP, Position: ms.Position,
			ScoreDelta: ms.ScoreDelta, GoldReward: ms.GoldReward,
		})
	}
	room := NewRoomFromSnapshot(snap.Name, snap.MaxPlayers, snap.Password, snap.SessionID, s, mobs)
	room.Kind = snap.Kind
	s.Rooms[room.Name] = room
	fmt.Printf("从 Redis 恢复房间 '%s'（怪 %d 只）\n", room.Name, len(mobs))
	return room
}
