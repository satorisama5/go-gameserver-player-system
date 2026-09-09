package world

import (
	"testing"

	protocol "unityserverupgrade/internal/protocol"
)

func TestNoteEditApplyWhenVersionMatches(t *testing.T) {
	n := NewDefaultRaidNote()
	r := n.Apply("a", editIns("collab", 0, 0, "ab"))
	if !r.OK {
		t.Fatal(r.Payload.Reason)
	}
	if got := blockContent(r.Payload, "collab"); got != "ab" {
		t.Fatalf("got %q", got)
	}
	ver := blockVersion(r.Payload, "collab")
	r2 := n.Apply("b", editIns("collab", ver, 2, "cd"))
	if !r2.OK || blockContent(r2.Payload, "collab") != "abcd" {
		t.Fatalf("%v %q", r2.Payload.Reason, blockContent(r2.Payload, "collab"))
	}
}

func TestNoteEditVersionMismatchThenResync(t *testing.T) {
	n := NewDefaultRaidNote()
	r1 := n.Apply("alice", editIns("collab", 0, 0, "甲"))
	r2 := n.Apply("bob", editIns("collab", 0, 0, "乙"))
	if !r1.OK {
		t.Fatal("alice should succeed")
	}
	if r2.OK || r2.Payload.Reason != "version_mismatch" {
		t.Fatalf("bob should mismatch, got ok=%v reason=%s", r2.OK, r2.Payload.Reason)
	}
	if len(r2.Payload.Blocks) == 0 || blockContent(r2.Payload, "collab") != "甲" {
		t.Fatalf("mismatch must include snapshot 甲, got blocks=%d content=%q",
			len(r2.Payload.Blocks), blockContent(r2.Payload, "collab"))
	}
	ver := blockVersion(r2.Payload, "collab")
	r3 := n.Apply("bob", editIns("collab", ver, 1, "乙"))
	if !r3.OK || blockContent(r3.Payload, "collab") != "甲乙" {
		t.Fatalf("resync edit: %v %q", r3.Payload.Reason, blockContent(r3.Payload, "collab"))
	}
}

func TestNoteDeleteIncremental(t *testing.T) {
	n := NewDefaultRaidNote()
	_ = n.Apply("a", editIns("collab", 0, 0, "abcdef"))
	ver := 1
	r := n.Apply("a", editDel("collab", ver, 2, 2)) // 删 cd → abef
	if !r.OK || blockContent(r.Payload, "collab") != "abef" {
		t.Fatalf("%v %q", r.Payload.Reason, blockContent(r.Payload, "collab"))
	}
}

func editIns(block string, base, pos int, text string) protocol.NotePayload {
	return protocol.NotePayload{
		Op: "edit", NoteID: protocol.DefaultRaidNoteID, BlockID: block,
		BaseBlockVersion: base,
		TextOp:           &protocol.NoteTextOpDTO{Kind: "ins", Pos: pos, Text: text},
	}
}

func editDel(block string, base, pos, ln int) protocol.NotePayload {
	return protocol.NotePayload{
		Op: "edit", NoteID: protocol.DefaultRaidNoteID, BlockID: block,
		BaseBlockVersion: base,
		TextOp:           &protocol.NoteTextOpDTO{Kind: "del", Pos: pos, Len: ln},
	}
}

func blockContent(p protocol.NotePayload, id string) string {
	for _, b := range p.Blocks {
		if b.ID == id {
			return b.Content
		}
	}
	return ""
}

func blockVersion(p protocol.NotePayload, id string) int {
	for _, b := range p.Blocks {
		if b.ID == id {
			return b.Version
		}
	}
	return -1
}
