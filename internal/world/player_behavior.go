package world

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

const (
	defaultHP          = int64(100)
	defaultSkillDamage = int64(20)
	defaultCastRange   = float64(8.0)
	defaultCooldownMs  = int64(1200)
)

type buffTask struct {
	// userName: 该到期任务所属玩家。
	userName string
	// buffName: 该到期任务对应的 Buff 名称（如 atk_up / shield）。
	buffName string
	// version: Buff 版本号。用于避免“旧到期任务误删新 Buff”。
	version int64
	// rounds: 还需要再转多少整圈时间轮才真正到期。
	rounds int
}

// timingWheel 是房间内轻量时间轮，随 Room.Tick 推进。
type timingWheel struct {
	// slotNum: 槽位总数（时间轮大小）。
	slotNum int
	// current: 当前指针所在槽位索引。
	current int
	// slots: 二维切片。外层是槽位，内层是该槽位挂载的到期任务列表。
	slots [][]buffTask
}

// newTimingWheel 创建时间轮。
// 若传入 slotNum 非法，回退到默认 300 槽。
func newTimingWheel(slotNum int) *timingWheel {
	if slotNum <= 0 {
		slotNum = 300
	}
	return &timingWheel{
		slotNum: slotNum,
		slots:   make([][]buffTask, slotNum),
	}
}

// addAfterTicks 把任务挂到“ticks 之后”的槽位。
// 关键点：
// 1) 用 target 计算落在哪个槽位；
// 2) 用 rounds 记录需要再转几圈，解决“超出一圈时长”的任务。
func (tw *timingWheel) addAfterTicks(ticks int, task buffTask) {
	if ticks <= 0 {
		ticks = 1
	}
	target := (tw.current + ticks) % tw.slotNum   //tickers相当于步进的长度 ，用目前经过时间ms除以广播速率
	task.rounds = ticks / tw.slotNum
	tw.slots[target] = append(tw.slots[target], task)
}

// tick 推进时间轮一步并处理当前槽位任务。
// 处理逻辑：
// - rounds > 0: 说明还没到期，rounds-- 后留在当前槽等待下一圈；
// - rounds == 0: 说明到期，触发 onExpire 回调。
//参数类型是函数，接收buffTask，无返回值
func (tw *timingWheel) tick(onExpire func(task buffTask)) {
	tw.current = (tw.current + 1) % tw.slotNum
	cur := tw.slots[tw.current]
	tw.slots[tw.current] = tw.slots[tw.current][:0]
	for _, t := range cur {
		if t.rounds > 0 {
			t.rounds--
			tw.slots[tw.current] = append(tw.slots[tw.current], t)
			continue
		}
		onExpire(t)
	}
}

type PlayerBehaviorManager struct {
	// combatMu 保护高频战斗态：seq/cd/hp 等。
	combatMu sync.RWMutex
	// buffMu 保护 Buff 与时间轮相关状态。
	buffMu sync.RWMutex

	// 每个玩家战斗动作的最后序号；用于检测重复包与乱序包。
	lastBattleSeq map[string]int64
	// 统计玩家累计缺包数量（seq 跳号时增加），用于观测链路质量。
	lastGapCount  map[string]int64

	// activeBuffs[user][buff] = version。version 用于避免“旧到期任务”删掉“新版本 buff”。
	activeBuffs map[string]map[string]int64
	buffVersion map[string]map[string]int64
	wheel       *timingWheel

	// 示例战斗状态（最小版）。
	hpByUser         map[string]int64
	lastCastAtByUser map[string]int64 // unix milli
	nowMillis        int64
}

// NewPlayerBehaviorManager 初始化玩家行为管理器。
// 这个管理器挂在 Room 上，由 Room.Tick 驱动，负责：
// - 战斗动作包序号处理；
// - 命中/冷却/伤害；
// - Buff 生命周期（时间轮）。
// NewPlayerBehaviorManager 创建“房间级玩家行为管理器”。
// 设计意图：
// 1) 序号相关（lastBattleSeq/lastGapCount）：用于高并发动作包的重复、乱序、缺包感知。
// 2) Buff 相关（activeBuffs/buffVersion/wheel）：用于 Buff 生效与到期调度（时间轮驱动）。
// 3) 战斗状态相关（hpByUser/lastCastAtByUser）：用于最小战斗链路结算（HP 与冷却）。
// 说明：这些状态都在房间内维护，避免跨房间共享状态导致锁竞争与语义混乱。
func NewPlayerBehaviorManager() *PlayerBehaviorManager {
	return &PlayerBehaviorManager{
		// 每玩家“最后处理序号”
		lastBattleSeq:    make(map[string]int64),
		// 每玩家累计缺包数（观测链路质量）
		lastGapCount:     make(map[string]int64),
		// 每玩家激活 Buff（value = version）
		activeBuffs:      make(map[string]map[string]int64),
		// 每玩家每 Buff 的版本计数器
		buffVersion:      make(map[string]map[string]int64),
		// 300 槽时间轮（随房间 Tick 推进）
		wheel:            newTimingWheel(300),
		// 每玩家当前 HP
		hpByUser:         make(map[string]int64),
		// 每玩家上次施法时间（毫秒逻辑时钟）
		lastCastAtByUser: make(map[string]int64),
	}
}

// Tick 由房间主循环每帧调用（当前约 33ms）。
// 这里推进“逻辑时间”，并驱动 Buff 到期处理。
func (m *PlayerBehaviorManager) Tick() {
	m.buffMu.Lock()
	defer m.buffMu.Unlock()
	// 注意：这里没有读系统时钟，而是使用房间 tick 推进逻辑时钟，保证“房间内一致节奏”。
	m.nowMillis += 33
	m.wheel.tick(func(task buffTask) {
		userBuffs, ok := m.activeBuffs[task.userName]
		if !ok {
			return
		}
		curVersion, ok := userBuffs[task.buffName]
		if !ok {
			return
		}
		// 只有“任务版本 == 当前版本”才删除，防止旧任务误删新 Buff。
		if curVersion == task.version {
			delete(userBuffs, task.buffName)
		}
	})
}

// ensurePlayer 确保玩家在本管理器中的状态容器已初始化。
// 这里是“懒初始化”，避免玩家未参与战斗时占用额外内存。
func (m *PlayerBehaviorManager) ensureCombatPlayerLocked(name string) {
	if _, ok := m.hpByUser[name]; !ok {
		m.hpByUser[name] = defaultHP
	}
}

func (m *PlayerBehaviorManager) ensureBuffPlayerLocked(name string) {
	if _, ok := m.activeBuffs[name]; !ok {
		m.activeBuffs[name] = make(map[string]int64)
	}
	if _, ok := m.buffVersion[name]; !ok {
		m.buffVersion[name] = make(map[string]int64)
	}
}

// ProcessBattleCast 处理一次施法动作。
// 输入：
// - caster: 施法者；
// - target: 目标（可为空）；
// - seq: 动作序号（用于高并发去重与丢包检测）；
// - skillName: 技能名（当前用于回包展示）。
// 输出：
// - 文本协议结果（方便当前客户端直接显示调试）。
func (m *PlayerBehaviorManager) ProcessBattleCast(caster, target UserEntity, seq int64, skillName string) string {
	casterName := caster.GetName()
	targetName := ""
	if target != nil {
		targetName = target.GetName()
	}

	// 第 0 步：战斗态初始化 + 高并发序号处理（只持有 combat 锁）。
	m.combatMu.Lock()
	m.ensureCombatPlayerLocked(casterName)
	if target != nil {
		m.ensureCombatPlayerLocked(targetName)
	}

	// 第 1 步：处理序号，做重复/乱序/缺包感知。
	last, exists := m.lastBattleSeq[casterName]
	if !exists {
		// 首包直接记录 seq。
		m.lastBattleSeq[casterName] = seq
	} else {
		// seq <= last：重复发包或乱序包，直接拒绝。
		if seq <= last {
			m.combatMu.Unlock()
			return fmt.Sprintf("BATTLE_DUPLICATE_OR_OUT_OF_ORDER|seq=%d|last=%d|skill=%s", seq, last, skillName)
		}
		// gap > 0 代表中间有缺包。
		gap := seq - last - 1
		m.lastBattleSeq[casterName] = seq
		if gap > 0 {
			m.lastGapCount[casterName] += gap
			m.combatMu.Unlock()
			// 简化 KCP 快速重传思路：检测到 gap 即立刻提示“缺失区间”。
			// 当前只返回提示，不做服务端缓存重放。
			return fmt.Sprintf("BATTLE_FAST_RESEND_REQUEST|from=%d|to=%d|current=%d|skill=%s",
				last+1, seq-1, seq, skillName)
		}
	}

	// 冷却判定：同一玩家在冷却窗口内不能连续施法。
	// 第 2 步：冷却判定。
	if m.nowMillis-m.lastCastAtByUser[casterName] < defaultCooldownMs {
		left := defaultCooldownMs - (m.nowMillis - m.lastCastAtByUser[casterName])
		m.combatMu.Unlock()
		return fmt.Sprintf("BATTLE_REJECT|reason=cooldown|left_ms=%d|skill=%s", left, skillName)
	}
	// 冷却通过后，更新施法时间点。
	m.lastCastAtByUser[casterName] = m.nowMillis
	m.combatMu.Unlock()

	// 第 3 步：无目标时仅视为施法成功（不做命中与伤害）。
	if target == nil {
		return fmt.Sprintf("BATTLE_CAST_OK|seq=%d|skill=%s|note=no_target", seq, skillName)
	}

	// 第 4 步：距离命中判定。
	distSq := distanceSq(caster.GetPosition().X, caster.GetPosition().Z, target.GetPosition().X, target.GetPosition().Z)
	if distSq > defaultCastRange*defaultCastRange {
		return fmt.Sprintf("BATTLE_MISS|seq=%d|skill=%s|target=%s|reason=out_of_range",
			seq, skillName, target.GetName())
	}

	// 第 5 步：服务端权威伤害（属性 + 装备加成 + 伤害检测）。
	atk := caster.GetATK()
	def := target.GetDEF()
	damage, ok, reason := CalcSkillDamage(atk, def, defaultSkillDamage, 0)
	if !ok {
		return FormatDamageReject(seq, skillName, reason)
	}
	// Buff 查询走独立 buff 读锁，避免与高频战斗态写锁竞争。
	m.buffMu.RLock()
	if _, ok := m.activeBuffs[casterName]["atk_up"]; ok {
		damage = int64(math.Round(float64(damage) * 1.2))
	}
	if _, ok := m.activeBuffs[targetName]["shield"]; ok {
		damage = int64(math.Round(float64(damage) * 0.7))
	}
	m.buffMu.RUnlock()

	// 第 6 步：扣减目标 HP，并做下限保护。
	m.combatMu.Lock()
	hp := m.hpByUser[targetName] - damage
	if hp < 0 {
		hp = 0
	}
	m.hpByUser[targetName] = hp
	m.combatMu.Unlock()

	line := fmt.Sprintf("BATTLE_HIT|seq=%d|skill=%s|target=%s|damage=%d|target_hp=%d|atk=%d|def=%d",
		seq, skillName, targetName, damage, hp, atk, def)
	if hp <= 0 {
		line += "|downed=1"
	}
	return line
}

// AddBuff 给玩家添加（或刷新）一个 Buff。
// 关键点：
// - 同名 Buff 每次添加都会生成新 version；
// - 到期任务记录 version，只有匹配当前 version 才会生效删除。
func (m *PlayerBehaviorManager) AddBuff(userName, buffName string, durationMs int64, roomTickMs int64) string {
	if durationMs <= 0 {
		durationMs = 3000
	}
	if roomTickMs <= 0 {
		roomTickMs = 33
	}

	m.buffMu.Lock()
	defer m.buffMu.Unlock()
	m.ensureBuffPlayerLocked(userName)

	// 版本递增：用于避免“旧到期任务”影响“新 Buff”。
	version := m.buffVersion[userName][buffName] + 1
	m.buffVersion[userName][buffName] = version
	m.activeBuffs[userName][buffName] = version

	// 毫秒时长转换为 tick 数。
	ticks := int(math.Ceil(float64(durationMs) / float64(roomTickMs)))
	// 到期任务写入时间轮；版本号用于保证“只过期当前版本”。
	m.wheel.addAfterTicks(ticks, buffTask{
		userName: userName,
		buffName: buffName,
		version:  version,
	})
	return fmt.Sprintf("BUFF_APPLY_OK|buff=%s|duration_ms=%d|version=%d", buffName, durationMs, version)
}

// ListBuffs 返回玩家当前激活 Buff 名称列表。
// 为了输出稳定，返回前做字典序排序。
func (m *PlayerBehaviorManager) ListBuffs(userName string) []string {
	m.buffMu.RLock()
	defer m.buffMu.RUnlock()
	userBuffs, ok := m.activeBuffs[userName]
	if !ok || len(userBuffs) == 0 {
		return nil
	}
	res := make([]string, 0, len(userBuffs))
	for buffName := range userBuffs {
		res = append(res, buffName)
	}
	sort.Strings(res)
	return res
}

// GetBattleState 返回玩家战斗摘要，方便压测/联调观察。
func (m *PlayerBehaviorManager) GetBattleState(userName string) string {
	m.combatMu.Lock()
	defer m.combatMu.Unlock()
	m.ensureCombatPlayerLocked(userName)
	lastSeq := m.lastBattleSeq[userName]
	gaps := m.lastGapCount[userName]
	hp := m.hpByUser[userName]
	return fmt.Sprintf("BATTLE_STATE|hp=%d|last_seq=%d|missing_total=%d", hp, lastSeq, gaps)
}

// GetHP 当前战斗 HP（未入战返回 defaultHP）。
func (m *PlayerBehaviorManager) GetHP(userName string) int64 {
	m.combatMu.Lock()
	defer m.combatMu.Unlock()
	m.ensureCombatPlayerLocked(userName)
	return m.hpByUser[userName]
}

// distanceSq 返回二维平面距离平方，避免开根号开销。
// 仅用于范围比较时，平方值足够。
func distanceSq(x1, z1, x2, z2 float32) float64 {
	dx := float64(x1 - x2)
	dz := float64(z1 - z2)
	return dx*dx + dz*dz
}
