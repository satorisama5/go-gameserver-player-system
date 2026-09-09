package world

import "testing"

func TestCalcSkillDamageNormal(t *testing.T) {
	d, ok, reason := CalcSkillDamage(15, 5, 20, 10)
	if !ok || d <= 0 {
		t.Fatalf("want ok damage, got ok=%v d=%d reason=%s", ok, d, reason)
	}
	// 20+15+10-2 = 43
	if d != 43 {
		t.Fatalf("got %d want 43", d)
	}
}

func TestCalcSkillDamageRejectAbnormalBonus(t *testing.T) {
	_, ok, reason := CalcSkillDamage(15, 5, 20, 9999)
	if ok || reason != "atk_bonus_abnormal" {
		t.Fatalf("want atk_bonus_abnormal, ok=%v reason=%s", ok, reason)
	}
}
