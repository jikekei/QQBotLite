package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type RPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}
type RPC struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      string          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *RPCError       `json:"error,omitempty"`
}
type PlayerEntry struct {
	Name string `json:"name"`
	ID   int    `json:"id"`
}
type GameResult struct {
	Name           string        `json:"name"`
	Online         int           `json:"online"`
	Max            int           `json:"max"`
	Admins         int           `json:"admins"`
	ClassD         int           `json:"class_d"`
	Scientists     int           `json:"scientists"`
	SCPs           int           `json:"scps"`
	RoundSeconds   int           `json:"round_seconds"`
	RoundCount     int           `json:"round_count"`
	RespawnSeconds *int          `json:"respawn_seconds,omitempty"`
	Players        []PlayerEntry `json:"players,omitempty"`
	Message        string        `json:"message,omitempty"`
}
type HelpEvent struct {
	ID     string `json:"event_id"`
	Player string `json:"player"`
	Text   string `json:"text"`
}
type gamePeer struct {
	conn    *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	pending map[string]chan RPC
	ready   bool
}

func (p *gamePeer) send(v any) error {
	p.writeMu.Lock()
	defer p.writeMu.Unlock()
	_ = p.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
	return p.conn.WriteJSON(v)
}

type Bridge struct {
	mu      sync.RWMutex
	servers map[string]ServerConfig
	peers   map[string]*gamePeer
	seq     atomic.Uint64
	prefix  string
	OnHelp  func(string, HelpEvent)
}

func NewBridge(servers []ServerConfig) *Bridge {
	b := &Bridge{servers: map[string]ServerConfig{}, peers: map[string]*gamePeer{}, prefix: strconv.FormatInt(time.Now().UnixNano(), 36)}
	for _, s := range servers {
		b.servers[s.ID] = s
	}
	return b
}
func (b *Bridge) Accept(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("server_id")
	s, ok := b.servers[id]
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || subtle.ConstantTimeCompare([]byte(token), []byte(s.Token)) != 1 {
		http.Error(w, "unauthorized", 401)
		return
	}
	b.mu.Lock()
	if b.peers[id] != nil {
		b.mu.Unlock()
		http.Error(w, "server already connected", 409)
		return
	}
	up := websocket.Upgrader{HandshakeTimeout: 5 * time.Second, CheckOrigin: func(r *http.Request) bool { return r.Header.Get("Origin") == "" }}
	conn, e := up.Upgrade(w, r, nil)
	if e != nil {
		b.mu.Unlock()
		return
	}
	p := &gamePeer{conn: conn, pending: map[string]chan RPC{}}
	b.peers[id] = p
	b.mu.Unlock()
	defer func() {
		_ = conn.Close()
		b.mu.Lock()
		if b.peers[id] == p {
			delete(b.peers, id)
		}
		b.mu.Unlock()
		p.mu.Lock()
		for _, ch := range p.pending {
			ch <- RPC{Error: &RPCError{-32001, "游戏服连接已断开"}}
		}
		p.pending = map[string]chan RPC{}
		p.mu.Unlock()
		log.Printf("[游戏服] %s 断开", s.Name)
	}()
	conn.SetReadLimit(1024 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
	conn.SetPongHandler(func(string) error { return conn.SetReadDeadline(time.Now().Add(60 * time.Second)) })
	done := make(chan struct{})
	defer close(done)
	go func() {
		t := time.NewTicker(20 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				_ = conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(5*time.Second))
			}
		}
	}()
	for {
		var m RPC
		if e = conn.ReadJSON(&m); e != nil {
			return
		}
		_ = conn.SetReadDeadline(time.Now().Add(60 * time.Second))
		if m.JSONRPC != "2.0" {
			return
		}
		if m.ID != "" && m.Method == "" {
			p.mu.Lock()
			ch := p.pending[m.ID]
			delete(p.pending, m.ID)
			p.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		switch m.Method {
		case "server.hello":
			var hello struct {
				Version int `json:"version"`
			}
			if json.Unmarshal(m.Params, &hello) != nil || hello.Version != 1 {
				return
			}
			p.mu.Lock()
			p.ready = true
			p.mu.Unlock()
			log.Printf("[游戏服] %s (id=%s) 已就绪", s.Name, id)
		case "server.heartbeat":
		case "server.help":
			p.mu.Lock()
			ready := p.ready
			p.mu.Unlock()
			var ev HelpEvent
			if ready && json.Unmarshal(m.Params, &ev) == nil && ev.ID != "" && len(ev.ID) <= 128 && len(ev.Player) <= 1024 && len(ev.Text) > 0 && len(ev.Text) <= 4096 && b.OnHelp != nil {
				b.OnHelp(id, ev)
			}
		default:
			if m.ID != "" {
				_ = p.send(RPC{JSONRPC: "2.0", ID: m.ID, Error: &RPCError{-32601, "unknown method"}})
			}
		}
	}
}
func (b *Bridge) Call(ctx context.Context, id, method string, params any) (GameResult, error) {
	b.mu.RLock()
	p := b.peers[id]
	b.mu.RUnlock()
	if p == nil {
		return GameResult{}, errors.New("服务器未连接")
	}
	p.mu.Lock()
	ready := p.ready
	p.mu.Unlock()
	if !ready {
		return GameResult{}, errors.New("服务器尚未就绪")
	}
	bytes, e := json.Marshal(params)
	if e != nil {
		return GameResult{}, e
	}
	requestID := b.prefix + "-" + strconv.FormatUint(b.seq.Add(1), 10)
	ch := make(chan RPC, 1)
	p.mu.Lock()
	p.pending[requestID] = ch
	p.mu.Unlock()
	defer func() { p.mu.Lock(); delete(p.pending, requestID); p.mu.Unlock() }()
	if e = p.send(RPC{JSONRPC: "2.0", ID: requestID, Method: method, Params: bytes}); e != nil {
		return GameResult{}, errors.New("游戏服发送失败")
	}
	select {
	case <-ctx.Done():
		return GameResult{}, errors.New("请求超时或已取消；管理操作不会自动重发")
	case reply := <-ch:
		if reply.Error != nil {
			return GameResult{}, fmt.Errorf("%s", reply.Error.Message)
		}
		var result GameResult
		if e = json.Unmarshal(reply.Result, &result); e != nil {
			return result, errors.New("游戏服返回格式错误")
		}
		return result, nil
	}
}
func (b *Bridge) Connected(id string) bool {
	b.mu.RLock()
	p := b.peers[id]
	b.mu.RUnlock()
	if p == nil {
		return false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ready
}
func (b *Bridge) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.peers {
		_ = p.conn.Close()
	}
}
