package storage

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/go-redis/redis/v8"
)

const (
	pvpCrossZKey      = "pvp:cross:z"
	pvpCrossMetaPref  = "pvp:cross:meta:"
	pvpCrossPendPref  = "pvp:cross:pending:"
	pvpCrossLockKey   = "pvp:cross:matchlock"
	pvpCrossMetaTTL   = 10 * time.Minute
	pvpCrossPendTTL   = 5 * time.Minute
	pvpCrossLockTTL   = 800 * time.Millisecond
)

// CrossPVPWaiter Redis 跨服天梯排队项。
type CrossPVPWaiter struct {
	UserID     string `json:"user_id"`
	Name       string `json:"name"`
	Rating     int    `json:"rating"`
	InstanceID string `json:"instance_id"`
	EnqueuedAt int64  `json:"enqueued_at_ms"`
}

// CrossPVPPending 匹配成功后待入房（客机迁移登录后消费）。
type CrossPVPPending struct {
	Room         string `json:"room"`
	HostInstance string `json:"host_instance"`
	HostTCP      string `json:"host_tcp"`
	TeamA        string `json:"team_a"`
	TeamB        string `json:"team_b"`
	OppName      string `json:"opp_name"`
}

// EnqueueCrossPVP 写入跨服天梯队列。
func EnqueueCrossPVP(w CrossPVPWaiter) error {
	if RDB == nil || w.UserID == "" || w.Name == "" || w.InstanceID == "" {
		return fmt.Errorf("cross pvp enqueue: incomplete")
	}
	if w.Rating <= 0 {
		w.Rating = 1000
	}
	if w.EnqueuedAt <= 0 {
		w.EnqueuedAt = time.Now().UnixMilli()
	}
	body, err := json.Marshal(w)
	if err != nil {
		return err
	}
	pipe := RDB.Pipeline()
	pipe.Set(Ctx, pvpCrossMetaPref+w.UserID, string(body), pvpCrossMetaTTL)
	pipe.ZAdd(Ctx, pvpCrossZKey, &redis.Z{Score: float64(w.Rating), Member: w.UserID})
	_, err = pipe.Exec(Ctx)
	return err
}

// CancelCrossPVP 出队。
func CancelCrossPVP(userID string) {
	if RDB == nil || userID == "" {
		return
	}
	pipe := RDB.Pipeline()
	pipe.Del(Ctx, pvpCrossMetaPref+userID)
	pipe.ZRem(Ctx, pvpCrossZKey, userID)
	_, _ = pipe.Exec(Ctx)
}

// TryLockCrossPVPMatch 短锁，避免多实例同时撮合。
func TryLockCrossPVPMatch() bool {
	if RDB == nil {
		return false
	}
	ok, err := RDB.SetNX(Ctx, pvpCrossLockKey, "1", pvpCrossLockTTL).Result()
	return err == nil && ok
}

func UnlockCrossPVPMatch() {
	if RDB == nil {
		return
	}
	RDB.Del(Ctx, pvpCrossLockKey)
}

// ListCrossPVPWaiters 按 rating 取排队者（上限 200）。
func ListCrossPVPWaiters() []CrossPVPWaiter {
	if RDB == nil {
		return nil
	}
	ids, err := RDB.ZRange(Ctx, pvpCrossZKey, 0, 199).Result()
	if err != nil || len(ids) == 0 {
		return nil
	}
	out := make([]CrossPVPWaiter, 0, len(ids))
	for _, id := range ids {
		raw, err := RDB.Get(Ctx, pvpCrossMetaPref+id).Result()
		if err != nil || raw == "" {
			_ = RDB.ZRem(Ctx, pvpCrossZKey, id).Err()
			continue
		}
		var w CrossPVPWaiter
		if json.Unmarshal([]byte(raw), &w) != nil || w.UserID == "" {
			continue
		}
		out = append(out, w)
	}
	return out
}

// SetCrossPVPPending 双方写入待入房信息。
func SetCrossPVPPending(userID string, p CrossPVPPending) {
	if RDB == nil || userID == "" {
		return
	}
	body, err := json.Marshal(p)
	if err != nil {
		return
	}
	_ = RDB.Set(Ctx, pvpCrossPendPref+userID, string(body), pvpCrossPendTTL).Err()
}

// TakeCrossPVPPending 读取并删除（登录入房用）。
func TakeCrossPVPPending(userID string) (CrossPVPPending, bool) {
	if RDB == nil || userID == "" {
		return CrossPVPPending{}, false
	}
	key := pvpCrossPendPref + userID
	raw, err := RDB.Get(Ctx, key).Result()
	if err != nil || raw == "" {
		return CrossPVPPending{}, false
	}
	RDB.Del(Ctx, key)
	var p CrossPVPPending
	if json.Unmarshal([]byte(raw), &p) != nil || p.Room == "" {
		return CrossPVPPending{}, false
	}
	return p, true
}

// PeekCrossPVPPending 只读不删（客机决定是否迁移）。
func PeekCrossPVPPending(userID string) (CrossPVPPending, bool) {
	if RDB == nil || userID == "" {
		return CrossPVPPending{}, false
	}
	raw, err := RDB.Get(Ctx, pvpCrossPendPref+userID).Result()
	if err != nil || raw == "" {
		return CrossPVPPending{}, false
	}
	var p CrossPVPPending
	if json.Unmarshal([]byte(raw), &p) != nil || p.Room == "" {
		return CrossPVPPending{}, false
	}
	return p, true
}
