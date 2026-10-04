package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type NapResponse struct {
	Status  string          `json:"status"`
	RetCode int             `json:"retcode"`
	Data    json.RawMessage `json:"data"`
	Echo    string          `json:"echo"`
}
type napSession struct {
	conn    *websocket.Conn
	mu      sync.Mutex
	writeMu sync.Mutex
	pending map[string]chan NapResponse
}
type NapCat struct {
	url, token string
	mu         sync.RWMutex
	session    *napSession
	seq        atomic.Uint64
}

func NewNapCat(url, token string) *NapCat { return &NapCat{url: url, token: token} }
func waitContext(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
func (n *NapCat) Run(ctx context.Context, handler func(context.Context, Message)) error {
	delay := time.Second
	for ctx.Err() == nil {
		h := http.Header{}
		if n.token != "" {
			h.Set("Authorization", "Bearer "+n.token)
		}
		conn, resp, e := (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).DialContext(ctx, n.url, h)
		if e != nil {
			if resp != nil {
				_ = resp.Body.Close()
				if resp.StatusCode == 401 || resp.StatusCode == 403 {
					return errors.New("NapCat 鉴权失败，请核对 Token 后重新启动")
				}
			}
			log.Printf("[NapCat] 连接失败；%s 后重试（检查 NapCat 是否登录并启用 WebSocket 服务端）", delay)
			if !waitContext(ctx, delay) {
				break
			}
			if delay < 30*time.Second {
				delay *= 2
				if delay > 30*time.Second {
					delay = 30 * time.Second
				}
			}
			continue
		}
		delay = time.Second
		n.serve(ctx, conn, handler)
		if !waitContext(ctx, delay) {
			break
		}
	}
	return nil
}
func (n *NapCat) serve(ctx context.Context, conn *websocket.Conn, handler func(context.Context, Message)) {
	s := &napSession{conn: conn, pending: map[string]chan NapResponse{}}
	n.mu.Lock()
	n.session = s
	n.mu.Unlock()
	log.Print("[NapCat] WebSocket 已连接")
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-done:
		}
	}()
	defer func() {
		n.mu.Lock()
		if n.session == s {
			n.session = nil
		}
		n.mu.Unlock()
		_ = conn.Close()
		s.mu.Lock()
		for _, ch := range s.pending {
			ch <- NapResponse{RetCode: -1}
		}
		s.pending = map[string]chan NapResponse{}
		s.mu.Unlock()
		log.Print("[NapCat] 连接断开")
	}()
	conn.SetReadLimit(2 * 1024 * 1024)
	for {
		_, b, e := conn.ReadMessage()
		if e != nil {
			return
		}
		var envelope struct {
			NapResponse
			PostType    string          `json:"post_type"`
			MessageType string          `json:"message_type"`
			MessageID   json.RawMessage `json:"message_id"`
			GroupID     json.RawMessage `json:"group_id"`
			UserID      json.RawMessage `json:"user_id"`
			SelfID      json.RawMessage `json:"self_id"`
			Message     json.RawMessage `json:"message"`
			Sender      struct {
				Role string `json:"role"`
			} `json:"sender"`
		}
		if json.Unmarshal(b, &envelope) != nil {
			continue
		}
		if envelope.Echo != "" {
			s.mu.Lock()
			ch := s.pending[envelope.Echo]
			delete(s.pending, envelope.Echo)
			s.mu.Unlock()
			if ch != nil {
				ch <- envelope.NapResponse
			}
			continue
		}
		if envelope.PostType != "message" {
			continue
		}
		uid := rawID(envelope.UserID)
		self := rawID(envelope.SelfID)
		if uid == "" || uid == self {
			continue
		}
		m := Message{Group: envelope.MessageType == "group", Target: uid, Sender: uid, ID: rawID(envelope.MessageID), Role: envelope.Sender.Role, Text: oneBotText(envelope.Message, self)}
		if m.Group {
			m.Target = rawID(envelope.GroupID)
		} else if envelope.MessageType != "private" {
			continue
		}
		go func(m Message) {
			if m.Group && m.Role == "" {
				lookup, cancel := context.WithTimeout(ctx, 3*time.Second)
				defer cancel()
				data, e := n.action(lookup, "get_group_member_info", map[string]any{"group_id": json.Number(m.Target), "user_id": json.Number(m.Sender), "no_cache": true})
				if e == nil {
					var member struct {
						Role string `json:"role"`
					}
					_ = json.Unmarshal(data, &member)
					m.Role = member.Role
				}
			}
			handler(ctx, m)
		}(m)
	}
}
func rawID(b json.RawMessage) string {
	if len(b) == 0 || string(b) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(b, &s) == nil {
		return s
	}
	var n json.Number
	if json.Unmarshal(b, &n) == nil {
		return n.String()
	}
	return ""
}
func oneBotText(raw json.RawMessage, self string) string {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		text = strings.ReplaceAll(text, "[CQ:at,qq="+self+"]", "")
		text = strings.ReplaceAll(text, "&#91;", "[")
		text = strings.ReplaceAll(text, "&#93;", "]")
		text = strings.ReplaceAll(text, "&#44;", ",")
		text = strings.ReplaceAll(text, "&amp;", "&")
		return strings.TrimSpace(text)
	}
	var segments []struct {
		Type string                     `json:"type"`
		Data map[string]json.RawMessage `json:"data"`
	}
	if json.Unmarshal(raw, &segments) != nil {
		return ""
	}
	var out strings.Builder
	for _, s := range segments {
		if s.Type == "text" {
			var t string
			_ = json.Unmarshal(s.Data["text"], &t)
			out.WriteString(t)
		}
	}
	return strings.TrimSpace(out.String())
}
func (n *NapCat) action(ctx context.Context, action string, params any) (json.RawMessage, error) {
	n.mu.RLock()
	s := n.session
	n.mu.RUnlock()
	if s == nil {
		return nil, errors.New("NapCat 尚未连接")
	}
	id := strconv.FormatUint(n.seq.Add(1), 10)
	ch := make(chan NapResponse, 1)
	s.mu.Lock()
	s.pending[id] = ch
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.pending, id); s.mu.Unlock() }()
	s.writeMu.Lock()
	_ = s.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	e := s.conn.WriteJSON(map[string]any{"action": action, "params": params, "echo": id})
	s.writeMu.Unlock()
	if e != nil {
		return nil, errors.New("NapCat 写入失败")
	}
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return nil, errors.New("NapCat 请求超时")
	case r := <-ch:
		if r.RetCode != 0 || r.Status == "failed" {
			return nil, fmt.Errorf("NapCat 操作被拒绝 retcode=%d；不缓存或补发", r.RetCode)
		}
		return r.Data, nil
	}
}
func (n *NapCat) sendMessage(ctx context.Context, m Message, segments any) error {
	p := map[string]any{"message": segments}
	action := "send_private_msg"
	if m.Group {
		action = "send_group_msg"
		p["group_id"] = json.Number(m.Target)
	} else {
		p["user_id"] = json.Number(m.Target)
	}
	_, e := n.action(ctx, action, p)
	return e
}
func (n *NapCat) Send(ctx context.Context, m Message, text string) error {
	return n.sendMessage(ctx, m, []any{map[string]any{"type": "text", "data": map[string]string{"text": text}}})
}
func (n *NapCat) Image(ctx context.Context, m Message, path string) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return errors.New("图片读取失败")
	}
	if len(b) > 8*1024*1024 {
		return errors.New("探针图片超过 8 MiB")
	}
	return n.sendMessage(ctx, m, []any{map[string]any{"type": "image", "data": map[string]string{"file": "base64://" + base64.StdEncoding.EncodeToString(b)}}})
}
