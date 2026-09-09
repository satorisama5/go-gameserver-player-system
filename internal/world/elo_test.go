package world

import "testing"

func TestEloUpsetGivesBigSwing(t *testing.T) {
	// 1400 vs 1000，高分赢只加很少；低分爆冷则大涨
	a, _ := EloUpdate(1400, 1000, 1, 32)
	if a-1400 > 5 {
		t.Fatalf("favorite win should gain little, got +%v", a-1400)
	}
	a2, b2 := EloUpdate(1400, 1000, 0, 32)
	if 1400-a2 < 20 {
		t.Fatalf("upset should punish favorite hard, got -%v", 1400-a2)
	}
	if b2-1000 < 20 {
		t.Fatalf("underdog should gain a lot, got +%v", b2-1000)
	}
}
