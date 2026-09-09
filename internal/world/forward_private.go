package world

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	protocol "unityserverupgrade/internal/protocol"
	storage "unityserverupgrade/internal/storage"
)

type forwardPMRequest struct {
	TargetName string `json:"target_name"`
	SenderName string `json:"sender_name"`
	Message    string `json:"message"`
}

type forwardPMResponse struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// CrossPVPForwardReq 跨服天梯撮合后的实例间通知。
type CrossPVPForwardReq struct {
	Action       string `json:"action"` // host_start | guest_invite
	Room         string `json:"room"`
	HostInstance string `json:"host_instance"`
	HostTCP      string `json:"host_tcp"`
	TeamA        string `json:"team_a"`
	TeamB        string `json:"team_b"`
	TargetName   string `json:"target_name"`
	OppName      string `json:"opp_name"`
}

type crossPVPForwardResp struct {
	OK  bool   `json:"ok"`
	Err string `json:"err,omitempty"`
}

// StartForwardServer 启动实例内转发HTTP接口：私聊 + 跨服天梯 + 选服列表。
func (s *Server) StartForwardServer() {
	mux := http.NewServeMux()
	mux.HandleFunc("/internal/forward/pm", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()

		var req forwardPMRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(forwardPMResponse{OK: false, Err: "bad json"})
			return
		}
		if req.TargetName == "" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(forwardPMResponse{OK: false, Err: "empty target"})
			return
		}

		s.MapLock.RLock()
		targetUser, ok := s.OnlineMap[req.TargetName]
		s.MapLock.RUnlock()
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(forwardPMResponse{OK: false, Err: "target not local"})
			return
		}

		formattedMsg := fmt.Sprintf("[私聊][%s对你说]: %s", req.SenderName, req.Message)
		targetUser.Send(protocol.PackTextMessage(formattedMsg))
		_ = json.NewEncoder(w).Encode(forwardPMResponse{OK: true})
	})

	mux.HandleFunc("/internal/servers", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		list := storage.ListGatewayMetas()
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(list)
	})

	mux.HandleFunc("/internal/forward/pvp_cross", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		defer r.Body.Close()
		var req CrossPVPForwardReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(crossPVPForwardResp{OK: false, Err: "bad json"})
			return
		}
		switch req.Action {
		case "host_start":
			s.handleCrossHostStart(req)
		case "guest_invite":
			s.handleCrossGuestInvite(req)
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(crossPVPForwardResp{OK: false, Err: "bad action"})
			return
		}
		_ = json.NewEncoder(w).Encode(crossPVPForwardResp{OK: true})
	})

	go func() {
		_ = http.ListenAndServe(s.ForwardListenAddr, mux)
	}()
}

func (s *Server) handleCrossHostStart(req CrossPVPForwardReq) {
	s.EnsureCrossPVPRoom(req.Room, req.TeamA, req.TeamB)
	s.MapLock.RLock()
	a, okA := s.OnlineMap[req.TeamA]
	b, okB := s.OnlineMap[req.TeamB]
	s.MapLock.RUnlock()
	s.RoomLock.RLock()
	room := s.Rooms[req.Room]
	s.RoomLock.RUnlock()
	if room == nil {
		return
	}
	msg := protocol.PackTextMessage(fmt.Sprintf(
		"PVP_MATCH|mode=cross|room=%s|a=%s|b=%s|host=%s",
		req.Room, req.TeamA, req.TeamB, req.HostInstance))
	if okA {
		storage.TakeCrossPVPPending(a.GetDbKey())
		a.MoveToRoom(room)
		a.Send(msg)
	}
	if okB {
		storage.TakeCrossPVPPending(b.GetDbKey())
		b.MoveToRoom(room)
		b.Send(msg)
	}
}

func (s *Server) handleCrossGuestInvite(req CrossPVPForwardReq) {
	s.MapLock.RLock()
	u, ok := s.OnlineMap[req.TargetName]
	s.MapLock.RUnlock()
	if !ok {
		return
	}
	u.Send(protocol.PackTextMessage(fmt.Sprintf(
		"PVP_CROSS_MATCH|tcp=%s|instance=%s|room=%s|opp=%s|hint=auto_migrate",
		req.HostTCP, req.HostInstance, req.Room, req.OppName)))
	go s.softMigrateUserForCrossPVP(u, req.HostTCP, req.HostInstance)
}

// softMigrateUserForCrossPVP 客机软迁移到主办实例。
func (s *Server) softMigrateUserForCrossPVP(u UserEntity, hostTCP, hostInstance string) {
	if hostTCP == "" || hostInstance == "" || hostInstance == s.InstanceID {
		return
	}
	time.Sleep(50 * time.Millisecond)
	u.BeginSoftMigrate(hostTCP, hostInstance, "pvp_cross")
}

func (s *Server) EnsureCrossPVPRoom(roomName, teamA, teamB string) *Room {
	s.RoomLock.Lock()
	defer s.RoomLock.Unlock()
	if r, ok := s.Rooms[roomName]; ok {
		return r
	}
	room := NewPVPRoom(roomName, 2, s, "pvp_cross", &PVPMatchState{
		Mode: MatchCross, TeamA: []string{teamA}, TeamB: []string{teamB},
	})
	s.Rooms[roomName] = room
	return room
}

// PostCrossPVPForward 向目标实例转发跨服天梯指令。
func PostCrossPVPForward(instanceID string, req CrossPVPForwardReq) error {
	addr, ok := storage.GetGatewayRoute(instanceID)
	if !ok {
		return fmt.Errorf("gateway route missing: %s", instanceID)
	}
	body, _ := json.Marshal(req)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post("http://"+addr+"/internal/forward/pvp_cross", "application/json", bytes.NewReader(body))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("forward status=%d", resp.StatusCode)
	}
	return nil
}

// DeliverPrivateMessage 根据 user->gateway 路由把私聊消息投递到本机或异机。
func (s *Server) DeliverPrivateMessage(targetName, senderName, safeMsg string) error {
	s.MapLock.RLock()
	targetUser, ok := s.OnlineMap[targetName]
	s.MapLock.RUnlock()
	if ok {
		formattedMsg := fmt.Sprintf("[私聊][%s对你说]: %s", senderName, safeMsg)
		targetUser.Send(protocol.PackTextMessage(formattedMsg))
		return nil
	}

	targetGatewayID, ok := storage.GetUserRoute(targetName)
	if !ok {
		return fmt.Errorf("target offline")
	}
	if targetGatewayID == s.InstanceID {
		return fmt.Errorf("target not found in local map")
	}
	targetAddr, ok := storage.GetGatewayRoute(targetGatewayID)
	if !ok {
		return fmt.Errorf("target gateway route missing")
	}

	reqBody, _ := json.Marshal(forwardPMRequest{
		TargetName: targetName,
		SenderName: senderName,
		Message:    safeMsg,
	})

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Post("http://"+targetAddr+"/internal/forward/pm", "application/json", bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("forward post failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("forward status=%d", resp.StatusCode)
	}
	return nil
}
