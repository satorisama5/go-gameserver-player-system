package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	cfg "unityserverupgrade/internal/config"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

// EventLogRollup 按时间窗聚合后的快照，写入 event_log_rollups，便于趋势与面板。
type EventLogRollup struct {
	WindowStart   int64             `bson:"window_start"`
	WindowEnd     int64             `bson:"window_end"`
	GeneratedAt   int64             `bson:"generated_at"`
	TopEventTypes []NameCount       `bson:"top_event_types"`
	LevelCounts   map[string]int64 `bson:"level_counts"`
	TopInstances  []NameCount       `bson:"top_instances"`
	TotalEvents   int64             `bson:"total_events"`
}

// NameCount 聚合桶名称 + 计数。
type NameCount struct {
	Name  string `bson:"name" json:"name"`
	Count int64  `bson:"count" json:"count"`
}

// EventLogSummaryJSON HTTP /summary 响应体。
type EventLogSummaryJSON struct {
	From          int64             `json:"from"`
	To            int64             `json:"to"`
	TopEventTypes []NameCount       `json:"top_event_types"`
	LevelCounts   map[string]int64 `json:"level_counts"`
	TopInstances  []NameCount       `json:"top_instances"`
	TotalEvents   int64             `json:"total_events"`
}

// EnsureEventLogRollupIndexes window_start 唯一，避免同一分钟重复写入。
func EnsureEventLogRollupIndexes() error {
	if EventLogRollupCollection == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	idx := mongo.IndexModel{
		Keys: bson.D{{Key: "window_start", Value: 1}},
		Options: options.Index().
			SetUnique(true).
			SetName("idx_event_log_rollups_window_start_unique"),
	}
	_, err := EventLogRollupCollection.Indexes().CreateOne(ctx, idx)
	return err
}

// ensureEventLogAnalyticsIndexes 补充 event_logs 上按类型/级别/实例聚合常用的索引。
func ensureEventLogAnalyticsIndexes() error {
	if EventLogCollection == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	models := []mongo.IndexModel{
		{
			Keys: bson.D{{Key: "event_type", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_type_created"),
		},
		{
			Keys: bson.D{{Key: "level", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_level_created"),
		},
		{
			Keys: bson.D{{Key: "instance_id", Value: 1}, {Key: "created_at", Value: -1}},
			Options: options.Index().
				SetName("idx_event_logs_instance_created"),
		},
	}
	_, err := EventLogCollection.Indexes().CreateMany(ctx, models)
	return err
}

func matchCreatedRange(start, end int64) bson.M {
	return bson.M{"created_at": bson.M{"$gte": start, "$lt": end}}
}

func aggregateNameCounts(ctx context.Context, start, end int64, field string, limit int64) ([]NameCount, error) {
	if EventLogCollection == nil {
		return nil, fmt.Errorf("event_logs 未初始化")
	}
	pipe := mongo.Pipeline{
		{{Key: "$match", Value: matchCreatedRange(start, end)}},
		{{Key: "$group", Value: bson.M{"_id": "$" + field, "count": bson.M{"$sum": 1}}}},
		{{Key: "$sort", Value: bson.M{"count": -1}}},
		{{Key: "$limit", Value: limit}},
	}
	c, err := EventLogCollection.Aggregate(ctx, pipe)
	if err != nil {
		return nil, err
	}
	defer c.Close(ctx)
	var out []NameCount
	for c.Next(ctx) {
		var row struct {
			ID    interface{} `bson:"_id"`
			Count int64       `bson:"count"`
		}
		if err := c.Decode(&row); err != nil {
			continue
		}
		name := ""
		switch v := row.ID.(type) {
		case string:
			name = v
		case nil:
			name = ""
		default:
			name = fmt.Sprint(v)
		}
		out = append(out, NameCount{Name: name, Count: row.Count})
	}
	return out, c.Err()
}

func aggregateLevelCounts(ctx context.Context, start, end int64) (map[string]int64, int64, error) {
	if EventLogCollection == nil {
		return nil, 0, fmt.Errorf("event_logs 未初始化")
	}
	pipe := mongo.Pipeline{
		{{Key: "$match", Value: matchCreatedRange(start, end)}},
		{{Key: "$group", Value: bson.M{"_id": "$level", "count": bson.M{"$sum": 1}}}},
	}
	c, err := EventLogCollection.Aggregate(ctx, pipe)
	if err != nil {
		return nil, 0, err
	}
	defer c.Close(ctx)
	m := make(map[string]int64)
	var total int64
	for c.Next(ctx) {
		var row struct {
			ID    interface{} `bson:"_id"`
			Count int64       `bson:"count"`
		}
		if err := c.Decode(&row); err != nil {
			continue
		}
		k := ""
		if s, ok := row.ID.(string); ok {
			k = s
		} else if row.ID != nil {
			k = fmt.Sprint(row.ID)
		} else {
			k = ""
		}
		m[k] = row.Count
		total += row.Count
	}
	return m, total, c.Err()
}

// BuildEventLogSummary 对 [start,end) 时间窗做聚合（左闭右开，created_at 为 Unix 秒）。
func BuildEventLogSummary(ctx context.Context, start, end int64) (*EventLogSummaryJSON, error) {
	topTypes, err := aggregateNameCounts(ctx, start, end, "event_type", 20)
	if err != nil {
		return nil, err
	}
	topInst, err := aggregateNameCounts(ctx, start, end, "instance_id", 10)
	if err != nil {
		return nil, err
	}
	levels, total, err := aggregateLevelCounts(ctx, start, end)
	if err != nil {
		return nil, err
	}
	return &EventLogSummaryJSON{
		From:          start,
		To:            end,
		TopEventTypes: topTypes,
		LevelCounts:   levels,
		TopInstances:  topInst,
		TotalEvents:   total,
	}, nil
}

// RunEventLogRollupForWindow 将上一完整时间窗写入 event_log_rollups（upsert）。
func RunEventLogRollupForWindow(ctx context.Context, windowStart, windowEnd int64) error {
	if EventLogRollupCollection == nil {
		return nil
	}
	sum, err := BuildEventLogSummary(ctx, windowStart, windowEnd)
	if err != nil {
		return err
	}
	doc := EventLogRollup{
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		GeneratedAt:   time.Now().Unix(),
		TopEventTypes: sum.TopEventTypes,
		LevelCounts:   sum.LevelCounts,
		TopInstances:  sum.TopInstances,
		TotalEvents:   sum.TotalEvents,
	}
	_, err = EventLogRollupCollection.UpdateOne(ctx,
		bson.M{"window_start": windowStart},
		bson.M{"$set": doc},
		options.Update().SetUpsert(true),
	)
	return err
}

func rollupLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().UTC()
		end := now.Truncate(interval)
		start := end.Add(-interval)
		if end.Unix() <= start.Unix() {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		err := RunEventLogRollupForWindow(ctx, start.Unix(), end.Unix())
		cancel()
		if err != nil {
			log.Printf("[eventlog_rollup] 失败 window=[%d,%d): %v", start.Unix(), end.Unix(), err)
		} else {
			log.Printf("[eventlog_rollup] 已写入 window=[%d,%d)", start.Unix(), end.Unix())
		}
	}
}

// 告警阈值（MVP 仅打日志，可调）。
const (
	alertErrorBurst5m     int64 = 50
	alertSingleType5m     int64 = 200
	alertMinTotalForRatio int64 = 30
	alertWarnRatio              = 0.45
)

func alertLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().Unix()
		start5 := now - 300
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		levels, total, err := aggregateLevelCounts(ctx, start5, now)
		cancel()
		if err != nil {
			log.Printf("[eventlog_alert] 聚合失败: %v", err)
			continue
		}
		errCount := levels["error"]
		if errCount >= alertErrorBurst5m {
			log.Printf("[EVENTLOG_ALERT] 5分钟内 error 条数=%d (阈值>=%d)", errCount, alertErrorBurst5m)
		}
		warnCount := levels["warn"]
		if total >= alertMinTotalForRatio {
			ratio := float64(warnCount+errCount) / float64(total)
			if ratio >= alertWarnRatio {
				log.Printf("[EVENTLOG_ALERT] 5分钟内 warn+error 占比=%.2f (阈值>=%.2f) total=%d", ratio, alertWarnRatio, total)
			}
		}
		ctx2, c2 := context.WithTimeout(context.Background(), 20*time.Second)
		topTypes, err := aggregateNameCounts(ctx2, start5, now, "event_type", 1)
		c2()
		if err == nil && len(topTypes) > 0 && topTypes[0].Count >= alertSingleType5m {
			log.Printf("[EVENTLOG_ALERT] 5分钟内最高频 event_type=%s count=%d (阈值>=%d)",
				topTypes[0].Name, topTypes[0].Count, alertSingleType5m)
		}
	}
}

func writeJSON(w http.ResponseWriter, status int, v interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func handleEventLogTrace(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reqID := r.URL.Query().Get("req_id")
	if reqID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing req_id"})
		return
	}
	limit := int64(200)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	cur, err := EventLogCollection.Find(ctx,
		bson.M{"req_id": reqID},
		options.Find().SetSort(bson.D{{Key: "created_at", Value: 1}}).SetLimit(limit),
	)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer cur.Close(ctx)
	var rows []EventLog
	for cur.Next(ctx) {
		var e EventLog
		if cur.Decode(&e) != nil {
			continue
		}
		rows = append(rows, e)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"req_id": reqID, "count": len(rows), "events": rows})
}

func handleEventLogSummary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	hours := 1.0
	if s := r.URL.Query().Get("hours"); s != "" {
		if f, err := strconv.ParseFloat(s, 64); err == nil && f > 0 && f <= 168 {
			hours = f
		}
	}
	end := time.Now().Unix()
	start := end - int64(hours*3600)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	sum, err := BuildEventLogSummary(ctx, start, end)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, sum)
}

func handleEventLogRollups(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if EventLogRollupCollection == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "rollups disabled"})
		return
	}
	limit := int64(48)
	if s := r.URL.Query().Get("limit"); s != "" {
		if n, err := strconv.ParseInt(s, 10, 64); err == nil && n > 0 && n <= 168 {
			limit = n
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	cur, err := EventLogRollupCollection.Find(ctx, bson.M{},
		options.Find().SetSort(bson.D{{Key: "window_start", Value: -1}}).SetLimit(limit))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer cur.Close(ctx)
	var list []EventLogRollup
	for cur.Next(ctx) {
		var doc EventLogRollup
		if cur.Decode(&doc) != nil {
			continue
		}
		list = append(list, doc)
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{"count": len(list), "rollups": list})
}

func handleEventLogHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "module": "event_log_analytics"})
}

// StartEventLogAnalytics 启动 rollup、告警日志与只读 HTTP（默认 127.0.0.1:19090）。
func StartEventLogAnalytics() {
	ac := cfg.Conf.EventLogAnalytics
	if !ac.Enabled {
		log.Println("[eventlog_analytics] 未启用 (event_log_analytics.enabled=false)")
		return
	}
	if err := ensureEventLogAnalyticsIndexes(); err != nil {
		log.Printf("[eventlog_analytics] event_logs 分析索引创建失败(可忽略): %v", err)
	}
	rollupSec := ac.RollupIntervalSeconds
	if rollupSec <= 0 {
		rollupSec = 60
	}
	alertSec := ac.AlertIntervalSeconds
	if alertSec <= 0 {
		alertSec = 300
	}
	addr := ac.ListenAddr
	if addr == "" {
		addr = "127.0.0.1:19090"
	}

	go rollupLoop(time.Duration(rollupSec) * time.Second)
	go alertLoop(time.Duration(alertSec) * time.Second)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/eventlog/health", handleEventLogHealth)
	mux.HandleFunc("/debug/eventlog/trace", handleEventLogTrace)
	mux.HandleFunc("/debug/eventlog/summary", handleEventLogSummary)
	mux.HandleFunc("/debug/eventlog/rollups", handleEventLogRollups)

	go func() {
		log.Printf("[eventlog_analytics] HTTP 只读接口监听 %s (trace/summary/rollups)", addr)
		if err := http.ListenAndServe(addr, mux); err != nil {
			log.Printf("[eventlog_analytics] HTTP 退出: %v", err)
		}
	}()
}
