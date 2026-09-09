package storage

import (
	"context"
	"fmt"
	"log"
	"time"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	protocol "unityserverupgrade/internal/protocol"
)

// RoomNoteCollection 房间开荒笔记快照集合。
var RoomNoteCollection *mongo.Collection

// RoomNoteDoc Mongo 中的笔记快照（仅保留最新态）。
type RoomNoteDoc struct {
	RoomSessionID string               `bson:"room_session_id"`
	NoteID        string               `bson:"note_id"`
	DocVersion    int                  `bson:"doc_version"`
	Blocks        []protocol.NoteBlock `bson:"blocks"`
	UpdatedBy     string               `bson:"updated_by,omitempty"`
	UpdatedAt     int64                `bson:"updated_at"`
}

// EnsureRoomNoteIndexes 房间笔记唯一键 (room_session_id, note_id)。
func EnsureRoomNoteIndexes() error {
	if RoomNoteCollection == nil {
		return fmt.Errorf("RoomNoteCollection 未初始化")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := RoomNoteCollection.Indexes().CreateOne(ctx, mongo.IndexModel{
		Keys:    bson.D{{Key: "room_session_id", Value: 1}, {Key: "note_id", Value: 1}},
		Options: options.Index().SetUnique(true),
	})
	return err
}

// UpsertRoomNoteSnapshot 覆盖写入当前快照。
func UpsertRoomNoteSnapshot(doc RoomNoteDoc) error {
	if RoomNoteCollection == nil {
		return fmt.Errorf("RoomNoteCollection 未初始化")
	}
	if doc.RoomSessionID == "" {
		return fmt.Errorf("room_session_id 为空")
	}
	if doc.NoteID == "" {
		doc.NoteID = protocol.DefaultRaidNoteID
	}
	if doc.UpdatedAt == 0 {
		doc.UpdatedAt = time.Now().Unix()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	filter := bson.M{"room_session_id": doc.RoomSessionID, "note_id": doc.NoteID}
	opts := options.Replace().SetUpsert(true)
	_, err := RoomNoteCollection.ReplaceOne(ctx, filter, doc, opts)
	return err
}

// LoadRoomNoteSnapshot 按房间会话加载笔记；不存在返回 nil, nil。
func LoadRoomNoteSnapshot(roomSessionID, noteID string) (*RoomNoteDoc, error) {
	if RoomNoteCollection == nil {
		return nil, fmt.Errorf("RoomNoteCollection 未初始化")
	}
	if noteID == "" {
		noteID = protocol.DefaultRaidNoteID
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var doc RoomNoteDoc
	err := RoomNoteCollection.FindOne(ctx, bson.M{
		"room_session_id": roomSessionID,
		"note_id":         noteID,
	}).Decode(&doc)
	if err == mongo.ErrNoDocuments {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &doc, nil
}

// InitRoomNoteCollection 由 InitDB 调用；失败只打日志不阻断启动。
func InitRoomNoteCollection() {
	if DB == nil {
		return
	}
	RoomNoteCollection = DB.Collection("room_notes")
	if err := EnsureRoomNoteIndexes(); err != nil {
		log.Println("room_notes 索引创建失败(不影响启动):", err)
	}
}
