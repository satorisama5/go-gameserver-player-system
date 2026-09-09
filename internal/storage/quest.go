package storage

import (
	"context"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// QuestDef 任务模板（内存目录）。
type QuestDef struct {
	ID           string
	Title        string
	TargetKills  int64
	TargetDamage int64
	RewardGold   int64
}

var QuestCatalog = map[string]QuestDef{
	"kill_3": {
		ID: "kill_3", Title: "击杀 3 只怪物", TargetKills: 3, RewardGold: 50,
	},
	"deal_100": {
		ID: "deal_100", Title: "累计造成 100 点伤害", TargetDamage: 100, RewardGold: 30,
	},
}

const (
	QuestStatusNone      = ""
	QuestStatusAccepted  = "accepted"
	QuestStatusCompleted = "completed"
	QuestStatusClaimed   = "claimed"
)

// QuestProgress 玩家任务进度（Mongo）。
type QuestProgress struct {
	UserKey      string `bson:"user_key"`
	QuestID      string `bson:"quest_id"`
	Status       string `bson:"status"`
	KillCount    int64  `bson:"kill_count"`
	DamageDealt  int64  `bson:"damage_dealt"`
	ClaimEpoch   int64  `bson:"claim_epoch"` // 每次 reset +1，领奖 req_id 带 epoch 防重复
	UpdatedAt    int64  `bson:"updated_at"`
}

var QuestCollection *mongo.Collection

func InitQuestCollection() {
	if DB == nil {
		return
	}
	QuestCollection = DB.Collection("quest_progress")
	_, _ = QuestCollection.Indexes().CreateOne(context.Background(), mongo.IndexModel{
		Keys:    bson.D{{Key: "user_key", Value: 1}, {Key: "quest_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
}

func GetQuestDef(id string) (QuestDef, bool) {
	d, ok := QuestCatalog[id]
	return d, ok
}

func LoadQuestProgress(userKey, questID string) (*QuestProgress, error) {
	if QuestCollection == nil {
		return nil, fmt.Errorf("quest collection not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var p QuestProgress
	err := QuestCollection.FindOne(ctx, bson.M{"user_key": userKey, "quest_id": questID}).Decode(&p)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

func ListQuestProgress(userKey string) ([]QuestProgress, error) {
	if QuestCollection == nil {
		return nil, fmt.Errorf("quest collection not ready")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	cur, err := QuestCollection.Find(ctx, bson.M{"user_key": userKey})
	if err != nil {
		return nil, err
	}
	defer cur.Close(ctx)
	var out []QuestProgress
	if err := cur.All(ctx, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func upsertQuest(p *QuestProgress) error {
	if QuestCollection == nil {
		return fmt.Errorf("quest collection not ready")
	}
	p.UpdatedAt = time.Now().Unix()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, err := QuestCollection.UpdateOne(ctx,
		bson.M{"user_key": p.UserKey, "quest_id": p.QuestID},
		bson.M{"$set": p},
		options.Update().SetUpsert(true),
	)
	return err
}

// AcceptQuest 接取任务。
func AcceptQuest(userKey, questID string) (*QuestProgress, error) {
	def, ok := GetQuestDef(questID)
	if !ok {
		return nil, fmt.Errorf("unknown quest")
	}
	_ = def
	cur, err := LoadQuestProgress(userKey, questID)
	if err != nil {
		return nil, err
	}
	if cur != nil && (cur.Status == QuestStatusAccepted || cur.Status == QuestStatusCompleted || cur.Status == QuestStatusClaimed) {
		return nil, fmt.Errorf("quest already in progress or claimed; use reset first")
	}
	epoch := int64(0)
	if cur != nil {
		epoch = cur.ClaimEpoch
	}
	p := &QuestProgress{
		UserKey: userKey, QuestID: questID,
		Status: QuestStatusAccepted, ClaimEpoch: epoch,
	}
	if err := upsertQuest(p); err != nil {
		return nil, err
	}
	return p, nil
}

// AddQuestKillProgress 击杀进度；若达标则置 completed。
func AddQuestKillProgress(userKey string, delta int64) []QuestProgress {
	return bumpQuestProgress(userKey, delta, 0)
}

// AddQuestDamageProgress 伤害进度。
func AddQuestDamageProgress(userKey string, delta int64) []QuestProgress {
	return bumpQuestProgress(userKey, 0, delta)
}

func bumpQuestProgress(userKey string, killDelta, dmgDelta int64) []QuestProgress {
	list, err := ListQuestProgress(userKey)
	if err != nil {
		return nil
	}
	var changed []QuestProgress
	for i := range list {
		p := &list[i]
		if p.Status != QuestStatusAccepted {
			continue
		}
		def, ok := GetQuestDef(p.QuestID)
		if !ok {
			continue
		}
		dirty := false
		if killDelta > 0 && def.TargetKills > 0 {
			p.KillCount += killDelta
			dirty = true
		}
		if dmgDelta > 0 && def.TargetDamage > 0 {
			p.DamageDealt += dmgDelta
			dirty = true
		}
		if !dirty {
			continue
		}
		if def.TargetKills > 0 && p.KillCount >= def.TargetKills {
			p.Status = QuestStatusCompleted
		}
		if def.TargetDamage > 0 && p.DamageDealt >= def.TargetDamage {
			p.Status = QuestStatusCompleted
		}
		_ = upsertQuest(p)
		changed = append(changed, *p)
	}
	return changed
}

// ClaimQuestReward 完成校验 + 领奖占位（真正加币走 GrantQuestRewardWithLedger）。
// 返回 progress、是否已 claimed 态可领。
func ClaimQuestReward(userKey, questID string) (*QuestProgress, QuestDef, error) {
	def, ok := GetQuestDef(questID)
	if !ok {
		return nil, def, fmt.Errorf("unknown quest")
	}
	p, err := LoadQuestProgress(userKey, questID)
	if err != nil {
		return nil, def, err
	}
	if p == nil || p.Status == QuestStatusNone || p.Status == QuestStatusAccepted {
		return nil, def, fmt.Errorf("quest not completed")
	}
	if p.Status == QuestStatusClaimed {
		return p, def, fmt.Errorf("already claimed")
	}
	// 再校验目标
	if def.TargetKills > 0 && p.KillCount < def.TargetKills {
		return nil, def, fmt.Errorf("kill progress not enough")
	}
	if def.TargetDamage > 0 && p.DamageDealt < def.TargetDamage {
		return nil, def, fmt.Errorf("damage progress not enough")
	}
	return p, def, nil
}

// MarkQuestClaimed 领奖成功后标记。
func MarkQuestClaimed(userKey, questID string) error {
	p, err := LoadQuestProgress(userKey, questID)
	if err != nil || p == nil {
		return err
	}
	p.Status = QuestStatusClaimed
	return upsertQuest(p)
}

// ResetQuest 重置任务（可重新接取）。
func ResetQuest(userKey, questID string) (*QuestProgress, error) {
	if _, ok := GetQuestDef(questID); !ok {
		return nil, fmt.Errorf("unknown quest")
	}
	p, err := LoadQuestProgress(userKey, questID)
	if err != nil {
		return nil, err
	}
	epoch := int64(1)
	if p != nil {
		epoch = p.ClaimEpoch + 1
	}
	np := &QuestProgress{
		UserKey: userKey, QuestID: questID,
		Status: QuestStatusNone, ClaimEpoch: epoch,
	}
	if err := upsertQuest(np); err != nil {
		return nil, err
	}
	return np, nil
}
