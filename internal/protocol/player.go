package protocol

// CharacterAttrs 角色战斗/展示属性（嵌在 Player 内，再挂到会话 User）。
type CharacterAttrs struct {
	Level    int   `json:"level" bson:"level"`
	HP       int64 `json:"hp" bson:"hp"`
	MaxHP    int64 `json:"max_hp" bson:"max_hp"`
	MP       int64 `json:"mp" bson:"mp"`
	MaxMP    int64 `json:"max_mp" bson:"max_mp"`
	ATK      int64 `json:"atk" bson:"atk"`
	DEF      int64 `json:"def" bson:"def"`
	CritRate int   `json:"crit_rate" bson:"crit_rate"` // 0-100
	Rating   int   `json:"rating" bson:"rating"`       // ELO 天梯分，默认 1000
}

// Power 简易战力：展示/匹配参考（非 ELO；真竞技用 Rating）。
func (a CharacterAttrs) Power() int64 {
	return a.ATK*2 + a.DEF + a.MaxHP/10 + int64(a.Level)*10
}

// DefaultCharacterAttrs 新号默认属性。
func DefaultCharacterAttrs() CharacterAttrs {
	return CharacterAttrs{
		Level: 1, HP: 100, MaxHP: 100, MP: 50, MaxMP: 50,
		ATK: 15, DEF: 5, CritRate: 5, Rating: 1000,
	}
}
