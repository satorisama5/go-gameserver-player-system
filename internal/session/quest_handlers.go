package session

import (
	"fmt"
	"strings"

	"unityserverupgrade/internal/storage"
)

// HandleQuestCommand 任务链路：接取→进度→完成→领奖(幂等)→重置。
//
//	quest|list
//	quest|accept|kill_3
//	quest|claim|kill_3
//	quest|reset|kill_3
func (this *User) HandleQuestCommand(args string) bool {
	if !this.requireLoggedIn() {
		return true
	}
	parts := strings.SplitN(args, "|", 3)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		this.Send(PackTextMessage("quest|list | quest|accept|id | quest|claim|id | quest|reset|id"))
		return true
	}
	op := strings.ToLower(strings.TrimSpace(parts[0]))
	switch op {
	case "list":
		this.questList()
	case "accept":
		if len(parts) < 2 {
			this.Send(PackTextMessage("quest|accept|id"))
			return true
		}
		this.questAccept(strings.TrimSpace(parts[1]))
	case "claim":
		if len(parts) < 2 {
			this.Send(PackTextMessage("quest|claim|id"))
			return true
		}
		this.questClaim(strings.TrimSpace(parts[1]))
	case "reset":
		if len(parts) < 2 {
			this.Send(PackTextMessage("quest|reset|id"))
			return true
		}
		this.questReset(strings.TrimSpace(parts[1]))
	default:
		this.Send(PackTextMessage("未知 quest 子命令"))
	}
	return true
}

func (this *User) questList() {
	var lines []string
	for id, def := range storage.QuestCatalog {
		p, _ := storage.LoadQuestProgress(this.DbKey, id)
		status := storage.QuestStatusNone
		kills, dmg := int64(0), int64(0)
		if p != nil {
			status = p.Status
			kills, dmg = p.KillCount, p.DamageDealt
		}
		lines = append(lines, fmt.Sprintf("%s:%s:status=%s:kills=%d/%d:dmg=%d/%d:gold=%d",
			id, def.Title, status, kills, def.TargetKills, dmg, def.TargetDamage, def.RewardGold))
	}
	this.Send(PackTextMessage("QUEST_LIST|" + strings.Join(lines, ";")))
}

func (this *User) questAccept(questID string) {
	p, err := storage.AcceptQuest(this.DbKey, questID)
	if err != nil {
		this.Send(PackTextMessage("QUEST_REJECT|reason=" + err.Error()))
		return
	}
	this.Send(PackTextMessage(fmt.Sprintf("QUEST_ACCEPTED|id=%s|status=%s", p.QuestID, p.Status)))
}

func (this *User) questClaim(questID string) {
	p, def, err := storage.ClaimQuestReward(this.DbKey, questID)
	if err != nil {
		this.Send(PackTextMessage("QUEST_CLAIM_REJECT|reason=" + err.Error()))
		return
	}
	reqID := this.currentReqID
	if reqID == "" {
		reqID = fmt.Sprintf("quest_claim:%s:%s:%d", this.DbKey, questID, p.ClaimEpoch)
	}
	bal, dup, err := storage.GrantQuestRewardWithLedger(this.DbKey, this.Name, reqID, questID, def.RewardGold)
	if err != nil {
		this.Send(PackTextMessage("QUEST_CLAIM_FAIL|" + err.Error()))
		return
	}
	if !dup {
		_ = storage.MarkQuestClaimed(this.DbKey, questID)
	}
	tag := "QUEST_CLAIM_OK"
	if dup {
		tag = "QUEST_CLAIM_DUPLICATE"
	}
	this.Send(PackTextMessage(fmt.Sprintf("%s|id=%s|gold=%d|balance=%d|req_id=%s",
		tag, questID, def.RewardGold, bal, reqID)))
}

func (this *User) questReset(questID string) {
	p, err := storage.ResetQuest(this.DbKey, questID)
	if err != nil {
		this.Send(PackTextMessage("QUEST_RESET_REJECT|reason=" + err.Error()))
		return
	}
	this.Send(PackTextMessage(fmt.Sprintf("QUEST_RESET_OK|id=%s|epoch=%d", p.QuestID, p.ClaimEpoch)))
}
