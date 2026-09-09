package session

import (
	"fmt"
	"strings"

	"unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/storage/inventory"
)

// HandleBattleCast 处理对玩家或怪物的施法；命中后推进任务进度。
func (u *User) HandleBattleCast(args string) bool {
	if u.Room == nil {
		u.Send(PackTextMessage("你不在任何房间中，无法释放技能"))
		return true
	}
	parts := strings.Split(args, "|")
	if len(parts) < 3 {
		u.Send(PackTextMessage("战斗格式错误, 应为: bcast|seq|skill|target"))
		return true
	}

	var seq int64
	if _, err := fmt.Sscanf(strings.TrimSpace(parts[0]), "%d", &seq); err != nil {
		u.Send(PackTextMessage("战斗格式错误: seq 必须为数字"))
		return true
	}
	skill := strings.TrimSpace(parts[1])
	target := strings.TrimSpace(parts[2])
	if skill == "" || target == "" {
		u.Send(PackTextMessage("战斗格式错误: skill/target 不能为空"))
		return true
	}

	atkBonus := int64(0)
	if u.DbKey != "" {
		atkBonus = inventory.GetEquippedAtkBonus(u.DbKey)
	}
	result := u.Room.HandleBattleCastWithBonus(u, seq, skill, target, atkBonus)
	u.Send(PackTextMessage(result))

	u.onBattleResultForQuest(result)

	if strings.HasPrefix(result, "MOB_KILLED|") {
		u.tryGrantMobKillReward(result)
	}
	u.sendReqDone("cast")
	return true
}

// onBattleResultForQuest 战斗命中/击杀 → 更新任务进度（接取后才生效）。
func (u *User) onBattleResultForQuest(result string) {
	if u.DbKey == "" {
		return
	}
	if strings.HasPrefix(result, "DAMAGE_REJECT|") {
		return
	}
	var damage int64
	for _, seg := range strings.Split(result, "|") {
		if strings.HasPrefix(seg, "damage=") {
			fmt.Sscanf(strings.TrimPrefix(seg, "damage="), "%d", &damage)
		}
	}
	var changed []storage.QuestProgress
	if damage > 0 {
		changed = append(changed, storage.AddQuestDamageProgress(u.DbKey, damage)...)
	}
	if strings.HasPrefix(result, "MOB_KILLED|") {
		changed = append(changed, storage.AddQuestKillProgress(u.DbKey, 1)...)
	}
	for _, p := range changed {
		u.Send(PackTextMessage(fmt.Sprintf("QUEST_PROGRESS|id=%s|status=%s|kills=%d|damage=%d",
			p.QuestID, p.Status, p.KillCount, p.DamageDealt)))
	}
}

// tryGrantMobKillReward 解析 MOB_KILLED 行并自动发放击杀奖励（金币/榜分）。
func (u *User) tryGrantMobKillReward(mobKilledLine string) {
	if u.DbKey == "" {
		return
	}
	var killID, monsterID string
	var score int32
	var gold int64
	for _, seg := range strings.Split(mobKilledLine, "|") {
		if strings.HasPrefix(seg, "kill_id=") {
			killID = strings.TrimPrefix(seg, "kill_id=")
		} else if strings.HasPrefix(seg, "monster_id=") {
			monsterID = strings.TrimPrefix(seg, "monster_id=")
		} else if strings.HasPrefix(seg, "score=") {
			fmt.Sscanf(strings.TrimPrefix(seg, "score="), "%d", &score)
		} else if strings.HasPrefix(seg, "gold=") {
			fmt.Sscanf(strings.TrimPrefix(seg, "gold="), "%d", &gold)
		}
	}
	if killID == "" || monsterID == "" || score <= 0 || gold <= 0 {
		return
	}
	u.executeKillReward(killID, monsterID, score, gold)
}
