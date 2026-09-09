package world

import (
	"fmt"
	"math"

	protocol "unityserverupgrade/internal/protocol"
)

// NotifyPlayerDowned 玩家被击倒（HP<=0）时由战斗链路调用，尝试结算 PVP。
func (r *Room) NotifyPlayerDowned(victim, killer string) {
	if r == nil || r.PVP == nil {
		return
	}
	r.PVP.mu.Lock()
	defer r.PVP.mu.Unlock()
	if r.PVP.Settled {
		return
	}
	teamOf := func(name string) int {
		for _, n := range r.PVP.TeamA {
			if n == name {
				return 1
			}
		}
		for _, n := range r.PVP.TeamB {
			if n == name {
				return 2
			}
		}
		return 0
	}
	alive := func(team []string) bool {
		for _, n := range team {
			if n == victim {
				continue
			}
			if r.BehaviorManager == nil {
				return true
			}
			if r.BehaviorManager.GetHP(n) > 0 {
				return true
			}
		}
		return false
	}
	side := teamOf(victim)
	if side == 0 {
		return
	}
	if side == 1 && alive(r.PVP.TeamA) {
		return
	}
	if side == 2 && alive(r.PVP.TeamB) {
		return
	}
	r.PVP.Settled = true
	aWin := side == 2 // B 队全灭 → A 胜
	r.settlePVPLocked(aWin)
}

func (r *Room) settlePVPLocked(teamAWins bool) {
	if r.PVP.Mode != MatchLadder && r.PVP.Mode != MatchCross {
		return
	}
	get := func(name string) UserEntity {
		return r.getMemberByName(name)
	}
	a, b := get(r.PVP.TeamA[0]), get(r.PVP.TeamB[0])
	if a == nil || b == nil {
		return
	}
	ra, rb := float64(a.GetRating()), float64(b.GetRating())
	sa := 1.0
	if !teamAWins {
		sa = 0
	}
	na, nb := EloUpdate(ra, rb, sa, DefaultEloK)
	apply := func(u UserEntity, nr float64) {
		ri := int(math.Round(nr))
		if ri < 0 {
			ri = 0
		}
		u.ApplyMatchRatings(ri, LevelFromRating(ri))
	}
	apply(a, na)
	apply(b, nb)
	msg := fmt.Sprintf("PVP_SETTLE|mode=%s|winner=%s|a=%s|rating=%d|b=%s|rating=%d",
		r.PVP.Mode,
		map[bool]string{true: a.GetName(), false: b.GetName()}[teamAWins],
		a.GetName(), a.GetRating(), b.GetName(), b.GetRating())
	packed := protocol.PackTextMessage(msg)
	a.Send(packed)
	b.Send(packed)
}
