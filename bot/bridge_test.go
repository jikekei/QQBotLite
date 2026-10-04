package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func connectGame(t *testing.T, b *Bridge, srv *httptest.Server, id, token string) *websocket.Conn {
	t.Helper()
	h := http.Header{}
	h.Set("Authorization", "Bearer "+token)
	ws, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?server_id="+id, h)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { ws.Close() })
	return ws
}
func awaitReady(t *testing.T, b *Bridge, id string) {
	t.Helper()
	end := time.Now().Add(3 * time.Second)
	for !b.Connected(id) {
		if time.Now().After(end) {
			t.Fatal("not ready")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestBridgeAuthenticationAndLifecycle(t *testing.T) {
	c := testConfig()
	b := NewBridge(c.Servers)
	srv := httptest.NewServer(http.HandlerFunc(b.Accept))
	defer srv.Close()
	defer b.Close()
	h := http.Header{}
	h.Set("Authorization", "Bearer wrong")
	_, resp, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?server_id=31140", h)
	if e == nil || resp == nil || resp.StatusCode != 401 {
		t.Fatalf("wrong-token handshake: response=%v error=%v", resp, e)
	}
	resp.Body.Close()
	ws := connectGame(t, b, srv, "31140", c.Servers[0].Token)
	if _, e = b.Call(context.Background(), "31140", "cx", struct{}{}); e == nil || !strings.Contains(e.Error(), "尚未就绪") {
		t.Fatal(e)
	}
	ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "server.hello", "params": map[string]int{"version": 1}})
	awaitReady(t, b, "31140")
	h.Set("Authorization", "Bearer "+c.Servers[0].Token)
	_, resp, e = websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"?server_id=31140", h)
	if e == nil || resp == nil || resp.StatusCode != 409 {
		t.Fatalf("duplicate handshake: response=%v error=%v", resp, e)
	}
	resp.Body.Close()
	go func() {
		var request RPC
		if ws.ReadJSON(&request) == nil {
			result, _ := json.Marshal(GameResult{Online: 7, Max: 25, Admins: 2})
			ws.WriteJSON(RPC{JSONRPC: "2.0", ID: request.ID, Result: result})
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	r, e := b.Call(ctx, "31140", "cx", struct{}{})
	if e != nil || r.Online != 7 || r.Admins != 2 {
		t.Fatalf("%+v %v", r, e)
	}
	short, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, e = b.Call(short, "31140", "kick", map[string]int{"player_id": 8}); e == nil {
		t.Fatal("missing timeout")
	}
	// Timeout produces exactly one request; the peer sees no management resend.
	var request RPC
	ws.SetReadDeadline(time.Now().Add(time.Second))
	if ws.ReadJSON(&request) != nil || request.Method != "kick" {
		t.Fatal(request)
	}
	ws.Close()
	end := time.Now().Add(time.Second)
	for b.Connected("31140") && time.Now().Before(end) {
		time.Sleep(time.Millisecond)
	}
	if b.Connected("31140") {
		t.Fatal("stale connection")
	}
}
func TestQueryOrderAndEmptyServer(t *testing.T) {
	c := testConfig()
	b := NewBridge(c.Servers)
	srv := httptest.NewServer(http.HandlerFunc(b.Accept))
	defer srv.Close()
	defer b.Close()
	ws := connectGame(t, b, srv, "31140", c.Servers[0].Token)
	ws.WriteJSON(map[string]any{"jsonrpc": "2.0", "method": "server.hello", "params": map[string]int{"version": 1}})
	awaitReady(t, b, "31140")
	go func() {
		for {
			var p RPC
			if ws.ReadJSON(&p) != nil {
				return
			}
			raw, _ := json.Marshal(GameResult{Max: 25, Players: []PlayerEntry{{"玩家", 9}}})
			ws.WriteJSON(RPC{JSONRPC: "2.0", ID: p.ID, Result: raw})
		}
	}()
	bot := NewBot(c, b, &fakeAdapter{}, "")
	text := bot.queryAll(context.Background(), "cx")
	if !strings.Contains(text, "在线人数：0/25") || !strings.Contains(text, "2服：服务器未连接") || !strings.Contains(text, "暖服") || strings.Index(text, "1服") > strings.Index(text, "2服") {
		t.Fatal(text)
	}
	if text = bot.list(context.Background(), 1); !strings.Contains(text, "玩家-9") {
		t.Fatal(text)
	}
}
