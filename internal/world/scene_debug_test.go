package world

import (
	"strings"
	"testing"

	config "unityserverupgrade/internal/config"
)

func TestSceneDebugDemoABC_ALosesBAfterWarp(t *testing.T) {
	config.Conf.AOI.GridSize = 50
	r := &Room{AOIManager: NewAOIManager(-2000, 2000, -2000, 2000, 50)}
	line := r.SceneDebugDemoABC()
	t.Log(line)

	if !strings.Contains(line, "T0 ") {
		t.Fatalf("missing T0: %s", line)
	}
	// T0：A 应看见 B（相邻格）
	if !strings.Contains(line, "A_see=A,B") && !strings.Contains(line, "A_see=B,A") {
		// seeList sorts names → A,B
		if !strings.Contains(line, "A_see=A,B") {
			t.Fatalf("T0 A should see B, got: %s", line)
		}
	}
	// T1：A 应在 resync 集合（B 离开旧九宫格）
	if !strings.Contains(line, "should_resync=") || !strings.Contains(line, "A") {
		t.Fatalf("T1 should mark A for resync: %s", line)
	}
	idx := strings.Index(line, "should_resync=")
	resyncPart := line[idx:]
	if end := strings.Index(resyncPart, "|T2"); end > 0 {
		resyncPart = resyncPart[:end]
	}
	if !strings.Contains(resyncPart, "A") {
		t.Fatalf("A missing from should_resync: %s", resyncPart)
	}
	// T2：A 视野不应再含 B
	t2 := line
	if i := strings.Index(line, "T2_after "); i >= 0 {
		t2 = line[i:]
	}
	if strings.Contains(t2, "A_see=A,B") || strings.Contains(t2, "A_see=B,A") {
		t.Fatalf("T2 A should NOT see B: %s", t2)
	}
	if !strings.Contains(t2, "A_see=A") {
		t.Fatalf("T2 A should still see self: %s", t2)
	}
}

func TestSceneDebugSnapshot_EmptyRoom(t *testing.T) {
	config.Conf.AOI.GridSize = 50
	r := &Room{AOIManager: NewAOIManager(-2000, 2000, -2000, 2000, 50), Members: map[string]UserEntity{}}
	line := r.SceneDebugSnapshot()
	if !strings.Contains(line, "SCENEDBG_SNAP") || !strings.Contains(line, "n=0") {
		t.Fatalf("unexpected snap: %s", line)
	}
}
