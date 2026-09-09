package world

import (
	"fmt"
	"sync"
	"time"

	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
)

// MatchMode 匹配模式。
type MatchMode string

const (
	MatchLadder MatchMode = "ladder" // 本服 1v1 ELO
	MatchCross  MatchMode = "cross"  // 跨服 1v1 ELO（Redis 队列）
)

const (
	ladderBucketSize   = 100 // 按 rating 每 100 分一桶
	ladderMatchTick    = 200 * time.Millisecond
	ladderDiffBase     = 200 // 刚进队
	ladderDiffAfter10s = 300
	ladderDiffAfter30s = 500
	ladderDiffAfter60s = 1000
)

// PVPMatchState 挂在临时 PVP 房上，击杀后结算。
type PVPMatchState struct {
	Mode    MatchMode
	TeamA   []string // ladder: 1 人
	TeamB   []string // ladder: 1 人
	Settled bool
	mu      sync.Mutex
}

type matchWaiter struct {
	User       UserEntity
	Rating     int
	EnqueuedAt time.Time
}

// Matchmaker 天梯匹配：本机 ladder + Redis 跨服 cross。
type Matchmaker struct {
	mu            sync.Mutex
	ladderBuckets map[int][]*matchWaiter // key = rating / ladderBucketSize
	ladderByName  map[string]int         // name -> bucket key，便于 O(1) 定位删除
	server        *Server
	stop          chan struct{}
}

func NewMatchmaker(s *Server) *Matchmaker {
	m := &Matchmaker{
		ladderBuckets: make(map[int][]*matchWaiter),
		ladderByName:  make(map[string]int),
		server:        s,
		stop:          make(chan struct{}),
	}
	go m.matchLoop()
	return m
}

func (m *Matchmaker) matchLoop() {
	t := time.NewTicker(ladderMatchTick)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			m.tickMatch()
		case <-m.stop:
			return
		}
	}
}

// tickMatch 定时撮合：本服桶 + 跨服 Redis。
func (m *Matchmaker) tickMatch() {
	m.mu.Lock()
	for m.tryMatchLadderOnceLocked() {
	}
	m.mu.Unlock()
	m.tryMatchCrossOnce()
}

func ladderBucketKey(rating int) int {
	if rating < 0 {
		rating = 0
	}
	return rating / ladderBucketSize
}

// ladderDiffCap 随等待时间扩大允许分差。
func ladderDiffCap(w *matchWaiter, now time.Time) int {
	wait := now.Sub(w.EnqueuedAt)
	switch {
	case wait >= 60*time.Second:
		return ladderDiffAfter60s
	case wait >= 30*time.Second:
		return ladderDiffAfter30s
	case wait >= 10*time.Second:
		return ladderDiffAfter10s
	default:
		return ladderDiffBase
	}
}

func canLadderPair(a, b *matchWaiter, now time.Time) bool {
	diff := a.Rating - b.Rating
	if diff < 0 {
		diff = -diff
	}
	cap := ladderDiffCap(a, now)
	if c := ladderDiffCap(b, now); c > cap {
		cap = c
	}
	return diff <= cap
}

func (m *Matchmaker) Enqueue(mode MatchMode, u UserEntity, rating int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(u.GetName())
	if rating <= 0 {
		rating = DefaultEloRating
	}
	w := &matchWaiter{User: u, Rating: rating, EnqueuedAt: time.Now()}
	switch mode {
	case MatchLadder:
		bk := ladderBucketKey(rating)
		m.ladderBuckets[bk] = append(m.ladderBuckets[bk], w)
		m.ladderByName[u.GetName()] = bk
		if m.tryMatchLadderOnceLocked() {
			return "matched"
		}
		return "queued"
	default:
		return "unknown_mode"
	}
}

// EnqueueCross 跨服天梯：写入 Redis，由各实例 tick 撮合。
func (m *Matchmaker) EnqueueCross(u UserEntity, rating int) string {
	if rating <= 0 {
		rating = DefaultEloRating
	}
	m.Cancel(u.GetName())
	storage.CancelCrossPVP(u.GetDbKey())
	err := storage.EnqueueCrossPVP(storage.CrossPVPWaiter{
		UserID:     u.GetDbKey(),
		Name:       u.GetName(),
		Rating:     rating,
		InstanceID: m.server.InstanceID,
		EnqueuedAt: time.Now().UnixMilli(),
	})
	if err != nil {
		return "queue_failed"
	}
	m.tryMatchCrossOnce()
	return "queued"
}

func (m *Matchmaker) Cancel(name string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeLocked(name)
}

// CancelAll 取消本服队列 + 跨服 Redis 队列。
func (m *Matchmaker) CancelAll(name, userID string) {
	m.Cancel(name)
	storage.CancelCrossPVP(userID)
}

func (m *Matchmaker) removeLocked(name string) {
	m.removeLadderLocked(name)
}

func (m *Matchmaker) removeLadderLocked(name string) {
	bk, ok := m.ladderByName[name]
	if !ok {
		return
	}
	list := m.ladderBuckets[bk]
	nlist := make([]*matchWaiter, 0, len(list))
	for _, w := range list {
		if w.User.GetName() != name {
			nlist = append(nlist, w)
		}
	}
	if len(nlist) == 0 {
		delete(m.ladderBuckets, bk)
	} else {
		m.ladderBuckets[bk] = nlist
	}
	delete(m.ladderByName, name)
}

// tryMatchLadderOnceLocked 只在「本人分差半径覆盖到的相邻桶」里找对手，不成对全表扫描。
func (m *Matchmaker) tryMatchLadderOnceLocked() bool {
	now := time.Now()
	for _, list := range m.ladderBuckets {
		for _, a := range list {
			cap := ladderDiffCap(a, now)
			lo := ladderBucketKey(a.Rating - cap)
			hi := ladderBucketKey(a.Rating + cap)
			for k := lo; k <= hi; k++ {
				for _, b := range m.ladderBuckets[k] {
					if a.User.GetName() == b.User.GetName() {
						continue
					}
					if !canLadderPair(a, b, now) {
						continue
					}
					wa, wb := a.User, b.User
					m.removeLadderLocked(a.User.GetName())
					m.removeLadderLocked(b.User.GetName())
					go m.server.startLadderMatch(wa, wb)
					return true
				}
			}
		}
	}
	return false
}

func (s *Server) startLadderMatch(a, b UserEntity) {
	roomName := fmt.Sprintf("pvp_l_%d", time.Now().UnixNano())
	s.RoomLock.Lock()
	room := NewPVPRoom(roomName, 2, s, "pvp_ladder", &PVPMatchState{
		Mode: MatchLadder, TeamA: []string{a.GetName()}, TeamB: []string{b.GetName()},
	})
	s.Rooms[roomName] = room
	s.RoomLock.Unlock()

	a.MoveToRoom(room)
	b.MoveToRoom(room)
	msg := protocol.PackTextMessage(fmt.Sprintf(
		"PVP_MATCH|mode=ladder|room=%s|a=%s|b=%s", roomName, a.GetName(), b.GetName()))
	a.Send(msg)
	b.Send(msg)
}

// tryMatchCrossOnce Redis 跨服天梯撮合（带短锁）。
func (m *Matchmaker) tryMatchCrossOnce() {
	if !storage.TryLockCrossPVPMatch() {
		return
	}
	defer storage.UnlockCrossPVPMatch()

	waiters := storage.ListCrossPVPWaiters()
	if len(waiters) < 2 {
		return
	}
	now := time.Now()
	used := map[string]bool{}
	for i := 0; i < len(waiters); i++ {
		a := waiters[i]
		if used[a.UserID] {
			continue
		}
		wa := &matchWaiter{Rating: a.Rating, EnqueuedAt: time.UnixMilli(a.EnqueuedAt)}
		for j := i + 1; j < len(waiters); j++ {
			b := waiters[j]
			if used[b.UserID] || a.UserID == b.UserID {
				continue
			}
			wb := &matchWaiter{Rating: b.Rating, EnqueuedAt: time.UnixMilli(b.EnqueuedAt)}
			if !canLadderPair(wa, wb, now) {
				continue
			}
			used[a.UserID] = true
			used[b.UserID] = true
			storage.CancelCrossPVP(a.UserID)
			storage.CancelCrossPVP(b.UserID)
			m.dispatchCrossMatch(a, b)
			return
		}
	}
}

func (m *Matchmaker) dispatchCrossMatch(a, b storage.CrossPVPWaiter) {
	host := a
	guest := b
	// 同实例：本机直接开房；异实例：选有 TCP 的一侧做 host，优先 a。
	if a.InstanceID != b.InstanceID {
		metaA, okA := storage.GetGatewayMeta(a.InstanceID)
		metaB, okB := storage.GetGatewayMeta(b.InstanceID)
		if okA && metaA.TCP != "" {
			host, guest = a, b
		} else if okB && metaB.TCP != "" {
			host, guest = b, a
		}
	}

	hostMeta, ok := storage.GetGatewayMeta(host.InstanceID)
	if !ok || hostMeta.TCP == "" {
		// 回队失败则丢弃本局（双方需重新排队）
		return
	}
	roomName := fmt.Sprintf("pvp_x_%d", time.Now().UnixNano())
	pendA := storage.CrossPVPPending{
		Room: roomName, HostInstance: host.InstanceID, HostTCP: hostMeta.TCP,
		TeamA: a.Name, TeamB: b.Name, OppName: b.Name,
	}
	pendB := storage.CrossPVPPending{
		Room: roomName, HostInstance: host.InstanceID, HostTCP: hostMeta.TCP,
		TeamA: a.Name, TeamB: b.Name, OppName: a.Name,
	}
	storage.SetCrossPVPPending(a.UserID, pendA)
	storage.SetCrossPVPPending(b.UserID, pendB)

	hostReq := CrossPVPForwardReq{
		Action: "host_start", Room: roomName, HostInstance: host.InstanceID, HostTCP: hostMeta.TCP,
		TeamA: a.Name, TeamB: b.Name,
	}
	guestReq := CrossPVPForwardReq{
		Action: "guest_invite", Room: roomName, HostInstance: host.InstanceID, HostTCP: hostMeta.TCP,
		TeamA: a.Name, TeamB: b.Name, TargetName: guest.Name, OppName: host.Name,
	}

	if a.InstanceID == b.InstanceID {
		// 同实例跨服队列偶发：直接本机开战（仍记 mode=cross）
		if a.InstanceID == m.server.InstanceID {
			m.server.handleCrossHostStart(hostReq)
		} else {
			_ = PostCrossPVPForward(a.InstanceID, hostReq)
		}
		return
	}

	if host.InstanceID == m.server.InstanceID {
		m.server.handleCrossHostStart(hostReq)
	} else {
		_ = PostCrossPVPForward(host.InstanceID, hostReq)
	}
	if guest.InstanceID == m.server.InstanceID {
		m.server.handleCrossGuestInvite(guestReq)
	} else {
		_ = PostCrossPVPForward(guest.InstanceID, guestReq)
	}
}
