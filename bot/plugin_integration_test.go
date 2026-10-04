package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestRealCSharpPluginCore(t *testing.T) {
	dll := os.Getenv("QQBOTLITE_PLUGIN_HARNESS")
	if dll == "" {
		t.Skip("set QQBOTLITE_PLUGIN_HARNESS to the built tests/PluginHarness DLL")
	}
	c := testConfig()
	b := NewBridge(c.Servers)
	srv := httptest.NewServer(http.HandlerFunc(b.Accept))
	defer srv.Close()
	defer b.Close()
	help := make(chan HelpEvent, 4)
	b.OnHelp = func(_ string, e HelpEvent) { help <- e }
	procCtx, kill := context.WithCancel(context.Background())
	cmd := exec.CommandContext(procCtx, "dotnet", dll, "ws"+strings.TrimPrefix(srv.URL, "http"), c.Servers[0].Token, "31140")
	if e := cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() { kill(); cmd.Wait() }()
	awaitReady(t, b, "31140")
	call := func(method string, args any) GameResult {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		r, e := b.Call(ctx, "31140", method, args)
		if e != nil {
			t.Fatal(method, e)
		}
		return r
	}
	r := call("info", struct{}{})
	if r.Online != 3 || r.RespawnSeconds == nil || *r.RespawnSeconds != 20 || r.RoundSeconds != 123 {
		t.Fatal(r)
	}
	select {
	case ev := <-help:
		if ev.Player != "测试玩家" || ev.Text != "需要管理" {
			t.Fatal(ev)
		}
	case <-time.After(time.Second):
		t.Fatal("C# help notification missing")
	}
	// Duplicate RPC IDs must replay the original response without kicking twice.
	b.mu.RLock()
	peer := b.peers["31140"]
	b.mu.RUnlock()
	sendRaw := func(id, method string) RPC {
		t.Helper()
		ch := make(chan RPC, 1)
		peer.mu.Lock()
		peer.pending[id] = ch
		peer.mu.Unlock()
		params := json.RawMessage(`{"player_id":7}`)
		if e := peer.send(RPC{JSONRPC: "2.0", ID: id, Method: method, Params: params}); e != nil {
			t.Fatal(e)
		}
		select {
		case r := <-ch:
			return r
		case <-time.After(time.Second):
			t.Fatal("C# response timeout")
			return RPC{}
		}
	}
	first := sendRaw("duplicate-kick", "kick")
	second := sendRaw("duplicate-kick", "kick")
	if string(first.Result) != string(second.Result) || !strings.Contains(string(first.Result), "kick:7:1") {
		t.Fatal(first, second)
	}
	bad := sendRaw("unknown", "ban")
	if bad.Error == nil || bad.Error.Code != -32601 {
		t.Fatal("extra management command accepted", bad)
	}
	call("bc", map[string]string{"text": "公告"})
	call("start", struct{}{})
	ack := call("rest", struct{}{})
	if !strings.Contains(ack.Message, "已受理") {
		t.Fatal(ack)
	}
	r = call("cx", struct{}{})
	if !strings.Contains(r.Message, "rest=0") {
		t.Fatal("restart occurred before acknowledgment delay", r.Message)
	}
	time.Sleep(550 * time.Millisecond)
	r = call("cx", struct{}{})
	if !strings.Contains(r.Message, "rest=1") || !strings.Contains(r.Message, "kicks=1") || !strings.Contains(r.Message, "bc=1") || !strings.Contains(r.Message, "notifications=1") {
		t.Fatal(r.Message)
	}
	ack = call("allrest", struct{}{})
	if !strings.Contains(ack.Message, "已受理") {
		t.Fatal(ack)
	}
	time.Sleep(550 * time.Millisecond)
	if r = call("cx", struct{}{}); !strings.Contains(r.Message, "allrest=1") {
		t.Fatal(r.Message)
	}
	select {
	case <-help:
		t.Fatal("cooldown repeated help")
	default:
	}
}
