package monster

import (
	"fmt"
	"sync"

	config "unityserverupgrade/internal/config"
	protocol "unityserverupgrade/internal/protocol"
)

// Manager 管理单个房间内的怪物列表。
type Manager struct {
	mu       sync.RWMutex
	monsters map[string]*Monster
}

func NewManager() *Manager {
	return &Manager{monsters: make(map[string]*Monster)}
}

func (m *Manager) SpawnDefaults(roomName string) {
	spawn := config.Conf.Server.SpawnPoint
	defs := []struct {
		suffix     string
		templateID string
		hp         int64
		dx, dz     float32
		gold       int64
		score      int32
	}{
		{"1", "slime", 80, 5, 0, 50, 10},
		{"2", "slime", 80, -5, 3, 50, 10},
		{"3", "wolf", 120, 0, -6, 80, 15},
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, d := range defs {
		name := fmt.Sprintf("Mob_%s_%s", d.templateID, d.suffix)
		m.monsters[name] = &Monster{
			ID:         fmt.Sprintf("%s_%s", roomName, name),
			TemplateID: d.templateID,
			Name:       name,
			HP:         d.hp,
			MaxHP:      d.hp,
			Position: protocol.PlayerPosition{
				X: spawn.X + d.dx,
				Y: spawn.Y,
				Z: spawn.Z + d.dz,
			},
			GoldReward: d.gold,
			ScoreDelta: d.score,
		}
	}
}

func (m *Manager) Get(name string) *Monster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.monsters[name]
}

func (m *Manager) All() map[string]*Monster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]*Monster, len(m.monsters))
	for k, v := range m.monsters {
		out[k] = v
	}
	return out
}

func (m *Manager) Remove(name string) *Monster {
	m.mu.Lock()
	defer m.mu.Unlock()
	mo := m.monsters[name]
	delete(m.monsters, name)
	return mo
}

// ApplyDamage 返回新 HP；若击杀返回 killed=true。
func (m *Manager) ApplyDamage(name string, damage int64) (hp int64, killed bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	mo, ok := m.monsters[name]
	if !ok || mo.HP <= 0 {
		return 0, false
	}
	mo.HP -= damage
	if mo.HP < 0 {
		mo.HP = 0
	}
	if mo.HP == 0 {
		return 0, true
	}
	return mo.HP, false
}

// Snapshot 导出怪物列表（房间持久化）。
func (m *Manager) Snapshot() []Monster {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Monster, 0, len(m.monsters))
	for _, mo := range m.monsters {
		if mo == nil {
			continue
		}
		out = append(out, *mo)
	}
	return out
}

// LoadFromSnapshot 用快照替换当前怪物表（不 SpawnDefaults）。
func (m *Manager) LoadFromSnapshot(list []Monster) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.monsters = make(map[string]*Monster, len(list))
	for i := range list {
		cp := list[i]
		m.monsters[cp.Name] = &cp
	}
}
