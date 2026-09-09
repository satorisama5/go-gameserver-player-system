package session

import protocol "unityserverupgrade/internal/protocol"

// Player 人物数据：挂在 User 上，属性与会话连接解耦。
type Player struct {
	Attrs protocol.CharacterAttrs
}

func NewDefaultPlayer() *Player {
	return &Player{Attrs: protocol.DefaultCharacterAttrs()}
}

func (p *Player) Power() int64 {
	if p == nil {
		return 0
	}
	return p.Attrs.Power()
}
