package world

import "fmt"

const (
	defaultSkillBaseDamage = int64(20)
	maxAtkBonusAllowed     = int64(500) // 装备加成上限，超视为异常
)

// CalcSkillDamage 服务端权威伤害：基础 + 攻 - 防/2，并做异常校验。
// 返回 applied / ok / reason（失败时 reason 非空）。
func CalcSkillDamage(atk, def, skillBase, atkBonus int64) (int64, bool, string) {
	if skillBase <= 0 {
		skillBase = defaultSkillBaseDamage
	}
	if atkBonus < 0 || atkBonus > maxAtkBonusAllowed {
		return 0, false, "atk_bonus_abnormal"
	}
	if atk < 0 || def < 0 {
		return 0, false, "attr_abnormal"
	}
	raw := skillBase + atk + atkBonus - def/2
	if raw < 1 {
		raw = 1
	}
	// 理论上限：不应超过「无防御满伤 + 少量余量」
	maxAllowed := skillBase + atk + atkBonus + 20
	if raw > maxAllowed {
		return 0, false, "damage_overflow"
	}
	return raw, true, ""
}

// FormatDamageReject 统一伤害校验失败回包。
func FormatDamageReject(seq int64, skill, reason string) string {
	return fmt.Sprintf("DAMAGE_REJECT|seq=%d|skill=%s|reason=%s", seq, skill, reason)
}
