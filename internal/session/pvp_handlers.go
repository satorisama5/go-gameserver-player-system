package session

import (
	"fmt"
	"strings"

	"unityserverupgrade/internal/storage"
	"unityserverupgrade/internal/world"
)

// HandlePVPCommand：
//
//	pvp|ladder   本服天梯 1v1（ELO）
//	pvp|cross    跨服天梯 1v1（Redis 队列，真打 + ELO；客机软迁移到主办实例）
//	pvp|cancel   取消本服/跨服排队
//	pvp|rating   查看 rating/level/power
func (this *User) HandlePVPCommand(args string) bool {
	if !this.requireLoggedIn() {
		return true
	}
	parts := strings.SplitN(args, "|", 2)
	op := strings.ToLower(strings.TrimSpace(parts[0]))
	if this.server.Matchmaker == nil {
		this.Send(PackTextMessage("PVP_REJECT|reason=matchmaker_unavailable"))
		return true
	}
	switch op {
	case "ladder":
		storage.CancelCrossPVP(this.DbKey)
		st := this.server.Matchmaker.Enqueue(world.MatchLadder, this, this.GetRating())
		this.Send(PackTextMessage(fmt.Sprintf("PVP_QUEUE|mode=ladder|status=%s|rating=%d|power=%d",
			st, this.GetRating(), this.GetCombatPower())))
	case "cross":
		this.server.Matchmaker.Cancel(this.Name)
		st := this.server.Matchmaker.EnqueueCross(this, this.GetRating())
		this.Send(PackTextMessage(fmt.Sprintf("PVP_QUEUE|mode=cross|status=%s|rating=%d|power=%d|instance=%s",
			st, this.GetRating(), this.GetCombatPower(), this.server.InstanceID)))
	case "cancel":
		this.server.Matchmaker.CancelAll(this.Name, this.DbKey)
		this.Send(PackTextMessage("PVP_CANCEL|ok"))
	case "rating", "info":
		this.Send(PackTextMessage(fmt.Sprintf("PVP_RATING|rating=%d|level=%d|power=%d",
			this.GetRating(), this.Player.Attrs.Level, this.GetCombatPower())))
	case "boss":
		this.Send(PackTextMessage("PVP_REJECT|reason=boss_mode_removed|hint=use_pvp|ladder_or_cross"))
	default:
		this.Send(PackTextMessage("pvp|ladder | pvp|cross | pvp|cancel | pvp|rating"))
	}
	return true
}

// tryJoinCrossPVPPending 登录后若有跨服待入房且本机是主办，则入 PVP 房。
func (this *User) tryJoinCrossPVPPending() {
	if this.DbKey == "" || this.server == nil {
		return
	}
	pend, ok := storage.TakeCrossPVPPending(this.DbKey)
	if !ok {
		return
	}
	if pend.HostInstance != this.server.InstanceID {
		// 误登到非主办：把 pending 写回并提示去正确 TCP
		storage.SetCrossPVPPending(this.DbKey, pend)
		this.Send(PackTextMessage(fmt.Sprintf(
			"PVP_CROSS_REDIRECT|tcp=%s|instance=%s|room=%s",
			pend.HostTCP, pend.HostInstance, pend.Room)))
		return
	}
	room := this.server.EnsureCrossPVPRoom(pend.Room, pend.TeamA, pend.TeamB)
	if room == nil {
		return
	}
	this.MoveToRoom(room)
	this.Send(PackTextMessage(fmt.Sprintf(
		"PVP_MATCH|mode=cross|room=%s|a=%s|b=%s|host=%s|joined=1",
		pend.Room, pend.TeamA, pend.TeamB, pend.HostInstance)))
}
