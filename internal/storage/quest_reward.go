package storage

import "fmt"

// GrantQuestRewardWithLedger 任务领奖：复用击杀奖励账本幂等（item_id=kill_reward:quest:{id}）。
// req_id 建议 quest_claim:{user}:{quest}:{epoch}，reset 后 epoch 变可再领。
func GrantQuestRewardWithLedger(userKey, userName, reqID, questID string, reward int64) (balanceAfter int64, duplicate bool, err error) {
	if questID == "" {
		return 0, false, fmt.Errorf("quest_id 不能为空")
	}
	return GrantKillRewardWithLedger(userKey, userName, reqID, "quest:"+questID, reward)
}
