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

func TestOneBotText(t *testing.T) {
	for _, raw := range []string{`"[CQ:at,qq=42] /cx"`, `[{"type":"at","data":{"qq":"42"}},{"type":"text","data":{"text":" /cx "}}]`} {
		if s := oneBotText(json.RawMessage(raw), "42"); s != "/cx" {
			t.Fatal(s)
		}
	}
	if rawID(json.RawMessage(`12345678901234567`)) != "12345678901234567" {
		t.Fatal("id precision")
	}
}
func TestNapCatTransportAndDeniedSend(t *testing.T) {
	serverDone := make(chan struct{})
	events := make(chan Message, 2)
	calls := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("auth")
		}
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		defer close(serverDone)
		ws.WriteJSON(map[string]any{"post_type": "message", "message_type": "group", "message_id": 123, "group_id": 456, "user_id": 789, "self_id": 42, "message": []any{map[string]any{"type": "text", "data": map[string]string{"text": "round start"}}}})
		for {
			var action struct {
				Action string `json:"action"`
				Echo   string `json:"echo"`
			}
			if ws.ReadJSON(&action) != nil {
				return
			}
			calls <- action.Action
			if action.Action == "get_group_member_info" {
				ws.WriteJSON(map[string]any{"echo": action.Echo, "retcode": 0, "status": "ok", "data": map[string]string{"role": "owner"}})
			} else {
				ws.WriteJSON(map[string]any{"echo": action.Echo, "retcode": 1200, "status": "failed"})
			}
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := NewNapCat("ws"+strings.TrimPrefix(srv.URL, "http"), "token")
	done := make(chan error, 1)
	go func() { done <- n.Run(ctx, func(_ context.Context, m Message) { events <- m }) }()
	select {
	case m := <-events:
		if !m.Group || m.Target != "456" || m.Sender != "789" || m.Role != "owner" || m.ID != "123" {
			t.Fatal(m)
		}
	case <-time.After(time.Second):
		t.Fatal("no message")
	}
	if e := n.Send(ctx, Message{Group: true, Target: "456"}, "help"); e == nil {
		t.Fatal("denied send accepted")
	}
	if <-calls != "get_group_member_info" || <-calls != "send_group_msg" {
		t.Fatal("actions")
	}
	select {
	case c := <-calls:
		t.Fatal("resend", c)
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("failed to stop")
	}
	<-serverDone
}
