package protocol

// NoteBlock 快照里的一块（纯文本视图）。
type NoteBlock struct {
	ID      string `json:"id"`
	Type    string `json:"type"` // text | todo | list | collab
	Content string `json:"content"`
	Version int    `json:"version"` // 该块全局版本；增量操作必须带匹配的 base
}

// NoteCursor 协同光标 / 选区（rune 下标）。
type NoteCursor struct {
	Actor   string `json:"actor"`
	BlockID string `json:"block_id"`
	Index   int    `json:"index"`
	Len     int    `json:"len,omitempty"` // 选区长度；0=仅光标
}

// NoteTextOpDTO 增量编辑：插入或删除一段（按 rune 下标）。
// Kind: "ins" | "del"
type NoteTextOpDTO struct {
	Kind string `json:"kind"`
	Pos  int    `json:"pos"`
	Text string `json:"text,omitempty"`
	Len  int    `json:"len,omitempty"`
}

// NotePayload C->S / S->C（靠 Op 区分）。
//
// 上行:
//   - get: 拉全量快照
//   - edit: 增量 ins/del，必须 BaseBlockVersion == 服务端当前 version；不一致则 reject+全量快照
//   - cursor: 光标/选区
//   - append: 兼容，末尾插入一行（仍走版本校验）
//   - patch / insert / delete: 整段替换或改结构
//
// 下行:
//   - snapshot / edit_applied / cursor / reject（reason=version_mismatch 时附带全量 Blocks）
type NotePayload struct {
	Op               string         `json:"op"`
	NoteID           string         `json:"note_id,omitempty"`
	BlockID          string         `json:"block_id,omitempty"`
	AfterBlockID     string         `json:"after_block_id,omitempty"`
	BaseDocVersion   int            `json:"base_doc_version,omitempty"`
	BaseBlockVersion int            `json:"base_block_version,omitempty"`
	Type             string         `json:"type,omitempty"`
	Content          string         `json:"content,omitempty"`
	DocVersion       int            `json:"doc_version,omitempty"`
	Blocks           []NoteBlock    `json:"blocks,omitempty"`
	TextOp           *NoteTextOpDTO `json:"text_op,omitempty"`
	Cursors          []NoteCursor   `json:"cursors,omitempty"`
	Index            int            `json:"index,omitempty"`
	SelLen           int            `json:"sel_len,omitempty"`
	Actor            string         `json:"actor,omitempty"`
	Reason           string         `json:"reason,omitempty"`
}

const DefaultRaidNoteID = "raid"

// DefaultCollabBlockID 默认同块增量协作区。
const DefaultCollabBlockID = "collab"
