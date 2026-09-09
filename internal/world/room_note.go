package world

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	protocol "unityserverupgrade/internal/protocol"
)

// 游戏向「增量广播」协同笔记（非 OT/CRDT）：
// - 客户端带 base_version + ins/del 片段；
// - 版本一致则服务端应用、version++、广播增量；
// - 版本不一致则 reject + 全量快照，客户端覆盖本地。

type noteCursorPresence struct {
	BlockID   string
	Index     int
	SelLen    int
	UpdatedAt time.Time
}

type noteBlockState struct {
	ID      string
	Type    string
	runes   []rune
	version int
}

func newBlock(id, typ, content string) *noteBlockState {
	return &noteBlockState{
		ID: id, Type: typ, runes: []rune(content), version: 0,
	}
}

func (b *noteBlockState) text() string { return string(b.runes) }
func (b *noteBlockState) len() int     { return len(b.runes) }

// RoomNote 房间开荒笔记（内存权威态）。
type RoomNote struct {
	NoteID     string
	DocVersion int // 结构变更用
	blocks     []*noteBlockState
	cursors    map[string]noteCursorPresence
	mu         sync.RWMutex
}

func NewDefaultRaidNote() *RoomNote {
	return &RoomNote{
		NoteID:     protocol.DefaultRaidNoteID,
		DocVersion: 1,
		cursors:    make(map[string]noteCursorPresence),
		blocks: []*noteBlockState{
			newBlock(protocol.DefaultCollabBlockID, "collab", ""),
			newBlock("boss_mech", "text", "Boss 1 阶段拉火，2 阶段集火"),
			newBlock("team_assignment", "text", "主坦 / 治疗 / 输出 待分工"),
			newBlock("consumables", "todo", "进图吃合剂"),
		},
	}
}

func (n *RoomNote) findBlockLocked(id string) *noteBlockState {
	for _, b := range n.blocks {
		if b.ID == id {
			return b
		}
	}
	return nil
}

func (n *RoomNote) snapshotLocked() protocol.NotePayload {
	blocks := make([]protocol.NoteBlock, 0, len(n.blocks))
	for _, b := range n.blocks {
		blocks = append(blocks, protocol.NoteBlock{
			ID: b.ID, Type: b.Type, Content: b.text(), Version: b.version,
		})
	}
	cursors := make([]protocol.NoteCursor, 0, len(n.cursors))
	for actor, c := range n.cursors {
		cursors = append(cursors, protocol.NoteCursor{
			Actor: actor, BlockID: c.BlockID, Index: c.Index, Len: c.SelLen,
		})
	}
	return protocol.NotePayload{
		Op: "snapshot", NoteID: n.NoteID, DocVersion: n.DocVersion,
		Blocks: blocks, Cursors: cursors,
	}
}

func (n *RoomNote) Snapshot() protocol.NotePayload {
	n.mu.RLock()
	defer n.mu.RUnlock()
	return n.snapshotLocked()
}

type NoteApplyResult struct {
	OK      bool
	Payload protocol.NotePayload
}

func (n *RoomNote) Apply(actor string, req protocol.NotePayload) NoteApplyResult {
	n.mu.Lock()
	defer n.mu.Unlock()

	noteID := req.NoteID
	if noteID == "" {
		noteID = protocol.DefaultRaidNoteID
	}
	if noteID != n.NoteID {
		return NoteApplyResult{OK: false, Payload: protocol.NotePayload{
			Op: "reject", NoteID: n.NoteID, Reason: "unknown_note_id", Actor: actor,
		}}
	}

	switch req.Op {
	case "get":
		p := n.snapshotLocked()
		p.Actor = actor
		return NoteApplyResult{OK: true, Payload: p}

	case "edit":
		return n.applyEditLocked(actor, req)

	case "append":
		blockID := req.BlockID
		if blockID == "" {
			blockID = protocol.DefaultCollabBlockID
		}
		line := strings.TrimSpace(req.Content)
		if line == "" {
			return n.rejectLocked(actor, "empty_content")
		}
		b := n.findBlockLocked(blockID)
		if b == nil {
			b = newBlock(blockID, "collab", "")
			n.blocks = append(n.blocks, b)
		}
		entry := fmt.Sprintf("[%s] %s", actor, line)
		if b.len() > 0 {
			entry = "\n" + entry
		}
		req.Op = "edit"
		req.BlockID = blockID
		req.BaseBlockVersion = b.version
		req.TextOp = &protocol.NoteTextOpDTO{Kind: "ins", Pos: b.len(), Text: entry}
		return n.applyEditLocked(actor, req)

	case "cursor":
		return n.applyCursorLocked(actor, req)

	case "patch":
		blockID := req.BlockID
		b := n.findBlockLocked(blockID)
		if b == nil {
			return n.rejectLocked(actor, "block_not_found")
		}
		if req.BaseBlockVersion != b.version {
			return n.versionMismatchLocked(actor, blockID)
		}
		b.runes = []rune(req.Content)
		b.version++
		n.DocVersion++
		n.clampCursorsLocked(blockID)
		return n.editAppliedLocked(actor, blockID, nil)

	case "insert":
		if req.BaseDocVersion != n.DocVersion {
			return n.docConflictLocked(actor)
		}
		if req.BlockID == "" {
			return n.rejectLocked(actor, "missing_block_id")
		}
		if n.findBlockLocked(req.BlockID) != nil {
			return n.rejectLocked(actor, "block_id_exists")
		}
		insertAt := len(n.blocks)
		if req.AfterBlockID != "" {
			for i, b := range n.blocks {
				if b.ID == req.AfterBlockID {
					insertAt = i + 1
					break
				}
			}
		}
		typ := req.Type
		if typ == "" {
			typ = "text"
		}
		nb := newBlock(req.BlockID, typ, req.Content)
		n.blocks = insertBlockState(n.blocks, insertAt, nb)
		n.DocVersion++
		p := n.snapshotLocked()
		p.Op = "applied"
		p.Actor = actor
		return NoteApplyResult{OK: true, Payload: p}

	case "delete":
		if req.BaseDocVersion != n.DocVersion {
			return n.docConflictLocked(actor)
		}
		idx := -1
		for i, b := range n.blocks {
			if b.ID == req.BlockID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return n.rejectLocked(actor, "block_not_found")
		}
		n.blocks = append(n.blocks[:idx], n.blocks[idx+1:]...)
		for a, c := range n.cursors {
			if c.BlockID == req.BlockID {
				delete(n.cursors, a)
			}
		}
		n.DocVersion++
		p := n.snapshotLocked()
		p.Op = "applied"
		p.Actor = actor
		return NoteApplyResult{OK: true, Payload: p}

	default:
		return n.rejectLocked(actor, "unknown_op")
	}
}

// applyEditLocked 增量广播核心：版本必须一致，否则全量重拉。
func (n *RoomNote) applyEditLocked(actor string, req protocol.NotePayload) NoteApplyResult {
	blockID := req.BlockID
	if blockID == "" {
		blockID = protocol.DefaultCollabBlockID
	}
	b := n.findBlockLocked(blockID)
	if b == nil {
		return n.rejectLocked(actor, "block_not_found")
	}
	if req.TextOp == nil {
		return n.rejectLocked(actor, "missing_text_op")
	}
	if req.BaseBlockVersion != b.version {
		return n.versionMismatchLocked(actor, blockID)
	}

	op := req.TextOp
	kind := strings.ToLower(op.Kind)
	switch kind {
	case "ins", "insert":
		if op.Text == "" {
			return n.rejectLocked(actor, "empty_insert")
		}
		pos := clamp(op.Pos, 0, b.len())
		ins := []rune(op.Text)
		out := make([]rune, 0, b.len()+len(ins))
		out = append(out, b.runes[:pos]...)
		out = append(out, ins...)
		out = append(out, b.runes[pos:]...)
		b.runes = out
		op.Pos = pos
		op.Kind = "ins"
		n.shiftCursorsLocked(blockID, pos, utf8.RuneCountInString(op.Text), true)
	case "del", "delete":
		if op.Len <= 0 {
			return n.rejectLocked(actor, "bad_delete_len")
		}
		pos := clamp(op.Pos, 0, b.len())
		end := pos + op.Len
		if end > b.len() {
			end = b.len()
		}
		op.Len = end - pos
		if op.Len <= 0 {
			return n.rejectLocked(actor, "bad_delete_len")
		}
		out := make([]rune, 0, b.len()-op.Len)
		out = append(out, b.runes[:pos]...)
		out = append(out, b.runes[end:]...)
		b.runes = out
		op.Pos = pos
		op.Kind = "del"
		n.shiftCursorsLocked(blockID, pos, op.Len, false)
	default:
		return n.rejectLocked(actor, "unknown_text_op")
	}

	b.version++
	n.DocVersion++
	return n.editAppliedLocked(actor, blockID, op)
}

func (n *RoomNote) applyCursorLocked(actor string, req protocol.NotePayload) NoteApplyResult {
	blockID := req.BlockID
	if blockID == "" {
		blockID = protocol.DefaultCollabBlockID
	}
	b := n.findBlockLocked(blockID)
	if b == nil {
		return n.rejectLocked(actor, "block_not_found")
	}
	idx := req.Index
	if idx < 0 {
		idx = 0
	}
	if idx > b.len() {
		idx = b.len()
	}
	sel := req.SelLen
	if sel < 0 {
		sel = 0
	}
	if idx+sel > b.len() {
		sel = b.len() - idx
	}
	n.cursors[actor] = noteCursorPresence{
		BlockID: blockID, Index: idx, SelLen: sel, UpdatedAt: time.Now(),
	}
	p := n.snapshotLocked()
	p.Op = "cursor"
	p.Actor = actor
	p.BlockID = blockID
	p.Index = idx
	p.SelLen = sel
	return NoteApplyResult{OK: true, Payload: p}
}

func (n *RoomNote) editAppliedLocked(actor, blockID string, textOp *protocol.NoteTextOpDTO) NoteApplyResult {
	p := n.snapshotLocked()
	p.Op = "edit_applied"
	p.Actor = actor
	p.BlockID = blockID
	p.TextOp = textOp
	if b := n.findBlockLocked(blockID); b != nil {
		p.BaseBlockVersion = b.version
		p.Content = b.text()
	}
	return NoteApplyResult{OK: true, Payload: p}
}

func (n *RoomNote) versionMismatchLocked(actor, blockID string) NoteApplyResult {
	p := n.snapshotLocked()
	p.Op = "reject"
	p.Reason = "version_mismatch"
	p.Actor = actor
	p.BlockID = blockID
	return NoteApplyResult{OK: false, Payload: p}
}

func (n *RoomNote) rejectLocked(actor, reason string) NoteApplyResult {
	p := n.snapshotLocked()
	p.Op = "reject"
	p.Reason = reason
	p.Actor = actor
	return NoteApplyResult{OK: false, Payload: p}
}

func (n *RoomNote) docConflictLocked(actor string) NoteApplyResult {
	p := n.snapshotLocked()
	p.Op = "reject"
	p.Reason = "doc_version_conflict"
	p.Actor = actor
	return NoteApplyResult{OK: false, Payload: p}
}

func (n *RoomNote) shiftCursorsLocked(blockID string, pos, delta int, insert bool) {
	for a, c := range n.cursors {
		if c.BlockID != blockID {
			continue
		}
		if insert {
			if c.Index >= pos {
				c.Index += delta
			}
		} else {
			end := pos + delta
			if c.Index >= end {
				c.Index -= delta
			} else if c.Index > pos {
				c.Index = pos
				c.SelLen = 0
			}
		}
		n.cursors[a] = c
	}
}

func (n *RoomNote) clampCursorsLocked(blockID string) {
	b := n.findBlockLocked(blockID)
	if b == nil {
		return
	}
	for a, c := range n.cursors {
		if c.BlockID != blockID {
			continue
		}
		if c.Index > b.len() {
			c.Index = b.len()
		}
		if c.Index+c.SelLen > b.len() {
			c.SelLen = b.len() - c.Index
		}
		n.cursors[a] = c
	}
}

func insertBlockState(blocks []*noteBlockState, index int, nb *noteBlockState) []*noteBlockState {
	if index < 0 {
		index = 0
	}
	if index > len(blocks) {
		index = len(blocks)
	}
	out := make([]*noteBlockState, 0, len(blocks)+1)
	out = append(out, blocks[:index]...)
	out = append(out, nb)
	out = append(out, blocks[index:]...)
	return out
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func PackNoteMessage(p protocol.NotePayload) []byte {
	data, _ := json.Marshal(p)
	msg, _ := json.Marshal(protocol.Message{ID: protocol.MSG_ID_NOTE, Data: data})
	return msg
}

func (this *Room) BroadcastNote(p protocol.NotePayload) {
	if this == nil {
		return
	}
	packed := PackNoteMessage(p)
	this.roomLock.RLock()
	defer this.roomLock.RUnlock()
	for _, member := range this.Members {
		member.Send(packed)
	}
}

func (this *Room) EnsureNote() *RoomNote {
	if this.Note == nil {
		this.Note = NewDefaultRaidNote()
	}
	return this.Note
}

// ApplyRoomNote 应用并广播；写成功后由 session 层刷 Redis / MQ。
func (this *Room) ApplyRoomNote(actor string, req protocol.NotePayload) NoteApplyResult {
	note := this.EnsureNote()
	result := note.Apply(actor, req)
	if !result.OK {
		return result
	}
	switch req.Op {
	case "edit", "append", "patch", "insert", "delete", "cursor":
		this.BroadcastNote(result.Payload)
	}
	return result
}

func (n *RoomNote) NoteDebugSummary() string {
	n.mu.RLock()
	defer n.mu.RUnlock()
	ver := 0
	if b := n.findBlockLocked(protocol.DefaultCollabBlockID); b != nil {
		ver = b.version
	}
	return fmt.Sprintf("note=%s doc_ver=%d collab_ver=%d blocks=%d", n.NoteID, n.DocVersion, ver, len(n.blocks))
}
