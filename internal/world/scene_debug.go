package world

import (
	"fmt"
	"sort"
	"strings"

	config "unityserverupgrade/internal/config"
)

// EnableSceneDebug 打开随后 N 次 BroadcastSceneState 的 SCENEDBG 输出（发给观察者 + 服务端日志）。
func (r *Room) EnableSceneDebug(ticks int) {
	if ticks <= 0 {
		ticks = 60 // 约 2s
	}
	if ticks > 600 {
		ticks = 600
	}
	r.sceneDebugLeft = ticks
}

func (r *Room) DisableSceneDebug() {
	r.sceneDebugLeft = 0
}

// SceneDebugSnapshot 当前房内每人：格子、九宫格可见名单（不发场景包，只给人看）。
func (r *Room) SceneDebugSnapshot() string {
	if r == nil || r.AOIManager == nil {
		return "SCENEDBG_SNAP|err=no_room"
	}
	r.roomLock.RLock()
	members := make([]UserEntity, 0, len(r.Members))
	for _, m := range r.Members {
		members = append(members, m)
	}
	r.roomLock.RUnlock()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("SCENEDBG_SNAP|tick=%d|n=%d", r.tickCount, len(members)))
	for _, viewer := range members {
		pos := viewer.GetPosition()
		gid := r.AOIManager.GetGridIDByPos(pos.X, pos.Z)
		near := r.AOIManager.GetPlayersInGrids(r.AOIManager.GetSurroundingGridIDs(gid))
		names := make([]string, 0, len(near)+1)
		names = append(names, viewer.GetName())
		seen := map[string]bool{viewer.GetName(): true}
		for _, u := range near {
			if seen[u.GetName()] {
				continue
			}
			seen[u.GetName()] = true
			names = append(names, u.GetName())
		}
		sort.Strings(names)
		b.WriteString(fmt.Sprintf("|%s@{%.0f,%.0f,%.0f}/gid=%d/see=%s",
			viewer.GetName(), pos.X, pos.Y, pos.Z, gid, strings.Join(names, ",")))
	}
	return b.String()
}

// SceneDebugDemoABC 不依赖多开客户端：用 A/B/C 假坐标推演跨格进出视野与应 resync 的人。
// grid_size=50 时：A 与 B 相邻格可见，C 很远；B 瞬移到 C 旁后，A 不应再看见 B，且 A 应在 resync 集合里。
func (r *Room) SceneDebugDemoABC() string {
	if r == nil || r.AOIManager == nil {
		return "SCENEDBG_DEMO|err=no_room"
	}
	gs := float32(config.Conf.AOI.GridSize)
	if gs <= 0 {
		gs = 50
	}
	type actor struct {
		name string
		x, z float32
	}
	inView := func(viewer, other actor) bool {
		if viewer.name == other.name {
			return true
		}
		vg := r.AOIManager.GetGridIDByPos(viewer.x, viewer.z)
		og := r.AOIManager.GetGridIDByPos(other.x, other.z)
		for _, id := range r.AOIManager.GetSurroundingGridIDs(vg) {
			if id == og {
				return true
			}
		}
		return false
	}
	seeList := func(viewer actor, all []actor) string {
		var names []string
		for _, o := range all {
			if inView(viewer, o) {
				names = append(names, o.name)
			}
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}
	resyncIfMove := func(mover actor, oldX, oldZ, newX, newZ float32, all []actor) string {
		oldGID := r.AOIManager.GetGridIDByPos(oldX, oldZ)
		newGID := r.AOIManager.GetGridIDByPos(newX, newZ)
		if oldGID == newGID {
			return "(no_cross_grid)"
		}
		oldNear := map[int]bool{}
		for _, id := range r.AOIManager.GetSurroundingGridIDs(oldGID) {
			oldNear[id] = true
		}
		newNear := map[int]bool{}
		for _, id := range r.AOIManager.GetSurroundingGridIDs(newGID) {
			newNear[id] = true
		}
		set := map[string]bool{mover.name: true}
		for _, o := range all {
			if o.name == mover.name {
				continue
			}
			og := r.AOIManager.GetGridIDByPos(o.x, o.z)
			// 旧位置：邻居仍在旧坐标上（mover 尚未挪）
			if oldNear[og] || newNear[og] {
				set[o.name] = true
			}
		}
		// mover 新位置后的 all：B 已在 new，重算新邻居
		for _, o := range all {
			if o.name == mover.name {
				continue
			}
			og := r.AOIManager.GetGridIDByPos(o.x, o.z)
			if newNear[og] {
				set[o.name] = true
			}
		}
		names := make([]string, 0, len(set))
		for n := range set {
			names = append(names, n)
		}
		sort.Strings(names)
		return strings.Join(names, ",")
	}

	a := actor{"A", 0, 0}
	b := actor{"B", gs, 0}      // 与 A 相邻格
	c := actor{"C", gs * 8, 0} // 远离
	all1 := []actor{a, b, c}

	var bld strings.Builder
	bld.WriteString(fmt.Sprintf("SCENEDBG_DEMO|grid=%v", gs))
	bld.WriteString(fmt.Sprintf("|T0 A_see=%s B_see=%s C_see=%s",
		seeList(a, all1), seeList(b, all1), seeList(c, all1)))

	// Tick1：B 瞬移到 C 旁边
	bOldX, bOldZ := b.x, b.z
	b.x, b.z = c.x+gs, c.z
	all2 := []actor{a, b, c}
	resync := resyncIfMove(actor{"B", bOldX, bOldZ}, bOldX, bOldZ, b.x, b.z, all1)
	bld.WriteString(fmt.Sprintf("|T1 B_warp=(%.0f,%.0f)->(%.0f,%.0f) should_resync=%s",
		bOldX, bOldZ, b.x, b.z, resync))
	bld.WriteString(fmt.Sprintf("|T2_after A_see=%s B_see=%s C_see=%s",
		seeList(a, all2), seeList(b, all2), seeList(c, all2)))
	bld.WriteString("|expect=A_lose_B;A_in_resync;next_full_clears_ghost")
	return bld.String()
}
