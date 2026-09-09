package world

import (
	"encoding/json"
	"fmt"

	"unityserverupgrade/internal/protocol"
	"unityserverupgrade/internal/world/monster"
)

const (
	defaultMonsterDamage = int64(20)
	defaultMonsterRange  = float64(12.0)
)

// castOnMonster 对房间内怪物施法（玩家目标未命中时尝试）。
// atkBonus 来自背包装备，由 session 层查询后传入。
func (r *Room) castOnMonster(caster UserEntity, seq int64, skillName, targetName string, atkBonus int64) string {
	if r.Monsters == nil {
		return "BATTLE_REJECT|reason=no_monsters"
	}
	mo := r.Monsters.Get(targetName)
	if mo == nil || mo.HP <= 0 {
		return fmt.Sprintf("BATTLE_MISS|seq=%d|skill=%s|target=%s|reason=not_found",
			seq, skillName, targetName)
	}

	// 复用玩家行为管理器的序号/冷却（目标为空仅做施法校验）
	prelude := r.BehaviorManager.ProcessBattleCast(caster, nil, seq, skillName)
	if !isCastPreludeOK(prelude) {
		return prelude
	}

	dx := float64(caster.GetPosition().X - mo.Position.X)
	dz := float64(caster.GetPosition().Z - mo.Position.Z)
	if dx*dx+dz*dz > defaultMonsterRange*defaultMonsterRange {
		return fmt.Sprintf("BATTLE_MISS|seq=%d|skill=%s|target=%s|reason=out_of_range",
			seq, skillName, targetName)
	}

	damage, ok, reason := CalcSkillDamage(caster.GetATK(), 0, defaultMonsterDamage, atkBonus)
	if !ok {
		return FormatDamageReject(seq, skillName, reason)
	}
	hp, killed := r.Monsters.ApplyDamage(targetName, damage)
	if killed {
		removed := r.Monsters.Remove(targetName)
		if removed != nil {
			return fmt.Sprintf("MOB_KILLED|seq=%d|skill=%s|target=%s|kill_id=%s|monster_id=%s|score=%d|gold=%d|damage=%d",
				seq, skillName, targetName, removed.ID, removed.TemplateID, removed.ScoreDelta, removed.GoldReward, damage)
		}
		return fmt.Sprintf("MOB_KILLED|seq=%d|skill=%s|target=%s|damage=%d", seq, skillName, targetName, damage)
	}
	return fmt.Sprintf("BATTLE_HIT|seq=%d|skill=%s|target=%s|damage=%d|target_hp=%d|kind=mob",
		seq, skillName, targetName, damage, hp)
}

func isCastPreludeOK(prelude string) bool {
	return len(prelude) >= 14 && prelude[:14] == "BATTLE_CAST_OK"
}

func monsterStatesForBroadcast(m *monster.Manager) map[string]protocol.MonsterState {
	if m == nil {
		return nil
	}
	all := m.All()
	out := make(map[string]protocol.MonsterState, len(all))
	for name, mo := range all {
		if mo == nil || mo.HP <= 0 {
			continue
		}
		out[name] = protocol.MonsterState{
			Name:       mo.Name,
			TemplateID: mo.TemplateID,
			HP:         mo.HP,
			MaxHP:      mo.MaxHP,
			Position:   mo.Position,
		}
	}
	return out
}

// broadcastMobKilledText 房内广播击杀提示（文本协议，便于客户端解析）。
func (r *Room) broadcastMobKilledText(line string) {
	packed := protocol.PackTextMessage(line)
	r.roomLock.RLock()
	defer r.roomLock.RUnlock()
	for _, member := range r.Members {
		member.Send(packed)
	}
}

// MobListJSON 返回当前房内怪物名列表（调试）。
func (r *Room) MobListJSON() string {
	if r.Monsters == nil {
		return "[]"
	}
	names := make([]string, 0)
	for name, mo := range r.Monsters.All() {
		if mo != nil && mo.HP > 0 {
			names = append(names, name)
		}
	}
	b, _ := json.Marshal(names)
	return string(b)
}
