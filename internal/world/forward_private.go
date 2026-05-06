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

// StartForwardServer 启动实例内转发HTTP接口，供其他网关把定向消息转发到本机。
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

	go func() {
		_ = http.ListenAndServe(s.ForwardListenAddr, mux)
	}()
}

// DeliverPrivateMessage 根据 user->gateway 路由把私聊消息投递到本机或异机。
func (s *Server) DeliverPrivateMessage(targetName, senderName, safeMsg string) error {
	// 本机优先
	s.MapLock.RLock()
	targetUser, ok := s.OnlineMap[targetName]
	s.MapLock.RUnlock()
	if ok {
		formattedMsg := fmt.Sprintf("[私聊][%s对你说]: %s", senderName, safeMsg)
		targetUser.Send(protocol.PackTextMessage(formattedMsg))
		return nil
	}

	// 查路由，决定异机转发
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

