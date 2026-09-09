package session

import (
	"strconv"
	"strings"

	"go.mongodb.org/mongo-driver/bson"

	mq "unityserverupgrade/internal/mq"
	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
	tool "unityserverupgrade/internal/tool"
	world "unityserverupgrade/internal/world"
)

func noteIsWriteOp(op string) bool {
	switch op {
	case "edit", "append", "patch", "insert", "delete":
		return true
	default:
		return false
	}
}

func (this *User) dispatchRoomNote(p *protocol.NotePayload) {
	if this.Room == nil {
		this.Send(world.PackNoteMessage(protocol.NotePayload{
			Op: "reject", NoteID: protocol.DefaultRaidNoteID, Reason: "not_in_room", Actor: this.Name,
		}))
		return
	}
	if p == nil {
		this.Send(world.PackNoteMessage(protocol.NotePayload{
			Op: "reject", Reason: "empty_payload", Actor: this.Name,
		}))
		return
	}

	op := strings.ToLower(strings.TrimSpace(p.Op))
	p.Op = op

	if noteIsWriteOp(op) || op == "cursor" {
		if op != "cursor" && !storage.AllowMessage(this.sessionUserKey()) {
			this.Send(world.PackNoteMessage(protocol.NotePayload{
				Op: "reject", NoteID: p.NoteID, Reason: "rate_limited", Actor: this.Name,
			}))
			return
		}
		if tool.WordFilter != nil && p.Content != "" {
			p.Content = tool.WordFilter.Handle(p.Content)
		}
		if tool.WordFilter != nil && p.TextOp != nil && p.TextOp.Text != "" {
			p.TextOp.Text = tool.WordFilter.Handle(p.TextOp.Text)
		}
	}

	result := this.Room.ApplyRoomNote(this.Name, *p)

	// get / reject：只回请求者（version_mismatch 时 payload 已带全量快照）
	if op == "get" || !result.OK {
		this.Send(world.PackNoteMessage(result.Payload))
		return
	}

	if noteIsWriteOp(op) {
		doc := storage.RoomNoteDoc{
			RoomSessionID: this.Room.SessionID,
			NoteID:        result.Payload.NoteID,
			DocVersion:    result.Payload.DocVersion,
			Blocks:        result.Payload.Blocks,
			UpdatedBy:     this.Name,
		}
		// Redis 热缓存全文；MQ 异步落 Mongo
		go storage.CacheRoomNoteSnapshot(doc.RoomSessionID, doc.NoteID, doc)
		go mq.PublishRoomNoteSnapshotToMQ(doc)
		storage.SaveEventLogAsync(storage.EventLog{
			EventType:  "note.applied",
			Level:      "info",
			ReqID:      this.currentReqID,
			UserKey:    this.DbKey,
			UserName:   this.Name,
			SessionID:  this.Room.SessionID,
			InstanceID: this.server.InstanceID,
			Message:    "room note applied",
			Meta: bson.M{
				"op":          op,
				"note_id":     result.Payload.NoteID,
				"doc_version": result.Payload.DocVersion,
				"block_id":    result.Payload.BlockID,
			},
		})
	}
}

// HandleNoteCommand 调试命令（增量广播模型）。
//
// 正确用法：先 get 拿到 version，再带同一 version 发 edit。
// 若两人抢同一 version，后到的会 version_mismatch，附带全量快照，客户端应覆盖本地。
//
//   note|get
//   note|ins|ver|pos|文本     并发/正式调试请带 ver
//   note|ins|pos|文本         串行：自动用当前最新 ver
//   note|del|ver|pos|len
//   note|cursor|index[|sel_len]
//   note|append|文本
func (this *User) HandleNoteCommand(args string) bool {
	parts := strings.SplitN(args, "|", 5)
	if len(parts) == 0 || strings.TrimSpace(parts[0]) == "" {
		this.Send(PackTextMessage("笔记: note|get | note|ins|ver|pos|文本 | note|del|ver|pos|len | note|cursor|index | note|append|文本"))
		return true
	}
	op := strings.ToLower(strings.TrimSpace(parts[0]))
	payload := protocol.NotePayload{Op: op, NoteID: protocol.DefaultRaidNoteID, BlockID: protocol.DefaultCollabBlockID}

	switch op {
	case "get":
		payload.Op = "get"
	case "append":
		if len(parts) < 2 {
			this.Send(PackTextMessage("note|append|文本"))
			return true
		}
		payload.Op = "append"
		payload.Content = strings.Join(parts[1:], "|")
	case "cursor":
		if len(parts) < 2 {
			this.Send(PackTextMessage("note|cursor|index 或 note|cursor|index|sel_len"))
			return true
		}
		idx, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			this.Send(PackTextMessage("index 必须是数字"))
			return true
		}
		payload.Op = "cursor"
		payload.Index = idx
		if len(parts) >= 3 && strings.TrimSpace(parts[2]) != "" {
			sel, err := strconv.Atoi(strings.TrimSpace(parts[2]))
			if err != nil {
				this.Send(PackTextMessage("sel_len 必须是数字"))
				return true
			}
			payload.SelLen = sel
		}
	case "ins", "insert":
		payload.Op = "edit"
		if len(parts) >= 4 && isAllDigits(parts[1]) && isAllDigits(parts[2]) {
			ver, _ := strconv.Atoi(strings.TrimSpace(parts[1]))
			pos, _ := strconv.Atoi(strings.TrimSpace(parts[2]))
			payload.BaseBlockVersion = ver
			payload.TextOp = &protocol.NoteTextOpDTO{Kind: "ins", Pos: pos, Text: strings.Join(parts[3:], "|")}
		} else if len(parts) >= 3 {
			pos, err := strconv.Atoi(strings.TrimSpace(parts[1]))
			if err != nil {
				this.Send(PackTextMessage("pos 必须是数字；请用 note|ins|ver|pos|文本"))
				return true
			}
			payload.BaseBlockVersion = -1
			payload.TextOp = &protocol.NoteTextOpDTO{Kind: "ins", Pos: pos, Text: strings.Join(parts[2:], "|")}
		} else {
			this.Send(PackTextMessage("note|ins|ver|pos|文本 或 note|ins|pos|文本"))
			return true
		}
	case "del", "delete":
		payload.Op = "edit"
		if len(parts) < 4 {
			this.Send(PackTextMessage("note|del|ver|pos|len"))
			return true
		}
		ver, err1 := strconv.Atoi(strings.TrimSpace(parts[1]))
		pos, err2 := strconv.Atoi(strings.TrimSpace(parts[2]))
		ln, err3 := strconv.Atoi(strings.TrimSpace(parts[3]))
		if err1 != nil || err2 != nil || err3 != nil {
			this.Send(PackTextMessage("ver/pos/len 必须是数字"))
			return true
		}
		payload.BaseBlockVersion = ver
		payload.TextOp = &protocol.NoteTextOpDTO{Kind: "del", Pos: pos, Len: ln}
	default:
		this.Send(PackTextMessage("未知笔记命令"))
		return true
	}

	if payload.Op == "edit" && payload.BaseBlockVersion < 0 && this.Room != nil && this.Room.Note != nil {
		snap := this.Room.Note.Snapshot()
		for _, b := range snap.Blocks {
			if b.ID == protocol.DefaultCollabBlockID {
				payload.BaseBlockVersion = b.Version
				break
			}
		}
		if payload.BaseBlockVersion < 0 {
			payload.BaseBlockVersion = 0
		}
	}

	this.dispatchRoomNote(&payload)
	return true
}

func isAllDigits(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
