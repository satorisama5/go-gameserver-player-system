package protocol

// MonsterState S->C 场景广播中的怪物状态（MSG_ID_SCENE_STATE / 2001）。
type MonsterState struct {
	Name       string         `json:"name"`
	TemplateID string         `json:"template_id"`
	HP         int64          `json:"hp"`
	MaxHP      int64          `json:"max_hp"`
	Position   PlayerPosition `json:"pos"`
}

// SceneStateBroadcast S->C 场景状态。
// Full=true：校准用全量（仍可按 AOI 裁剪接收者视野）。
// Full=false：仅脏实体增量。
type SceneStateBroadcast struct {
	Full     bool                    `json:"full"`
	Players  map[string]PlayerState  `json:"players,omitempty"`
	Monsters map[string]MonsterState `json:"monsters,omitempty"`
}
