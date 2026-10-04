package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

func TestOfficialIdentity(t *testing.T) {
	for _, role := range []string{"admin", "owner", "member", ""} {
		m, ok := officialMessage("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m","content":" /cx ","group_openid":"g","author":{"member_openid":"u","member_role":"`+role+`"}}`))
		if !ok || m.Sender != "u" || m.Role != role || m.Text != "/cx" || !m.Group {
			t.Fatal(m, ok)
		}
	}
	m, ok := officialMessage("C2C_MESSAGE_CREATE", json.RawMessage(`{"id":"m","author":{"user_openid":"u"},"content":"helloworld"}`))
	if !ok || m.Group || m.Target != "u" {
		t.Fatal(m)
	}
	if _, ok := officialMessage("GROUP_AT_MESSAGE_CREATE", json.RawMessage(`{"id":"m","group_openid":"g","author":{"member_openid":"u","bot":true}}`)); ok {
		t.Fatal("bot loop")
	}
}
func TestOfficialSendBudgetAndNoRetry(t *testing.T) {
	var mu sync.Mutex
	tokens := 0
	attempts := 0
	deny := false
	bodies := []map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/token" {
			tokens++
			w.Write([]byte(`{"access_token":"test","expires_in":"7200"}`))
			return
		}
		attempts++
		if r.Header.Get("Authorization") != "QQBot test" {
			t.Error("missing auth")
		}
		var p map[string]any
		json.NewDecoder(r.Body).Decode(&p)
		bodies = append(bodies, p)
		if deny {
			w.WriteHeader(403)
			w.Write([]byte(`{"code":306007}`))
			return
		}
		w.Write([]byte(`{"id":"sent","file_info":"image"}`))
	}))
	defer srv.Close()
	c := testConfig()
	c.Official.AppID = "app"
	c.Official.AppSecret = "secret"
	o := NewOfficial(c)
	o.base = srv.URL
	o.tokenURL = srv.URL + "/token"
	ctx := context.Background()
	m := Message{Group: true, Target: "g", ID: "m"}
	if e := o.Send(ctx, m, strings.Repeat("文", 5000)); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	if attempts != 5 || tokens != 1 {
		t.Fatal(attempts, tokens)
	}
	for i, p := range bodies {
		if p["msg_id"] != "m" || int(p["msg_seq"].(float64)) != i+1 || !utf8.ValidString(p["content"].(string)) {
			t.Fatal(p)
		}
	}
	mu.Unlock()
	path := filepath.Join(t.TempDir(), "a.png")
	os.WriteFile(path, []byte("test"), 0600)
	if e := o.Image(ctx, m, path); e == nil {
		t.Fatal("sixth reply allowed")
	}
	mu.Lock()
	if attempts != 5 {
		t.Fatal("must skip upload without reply budget")
	}
	deny = true
	mu.Unlock()
	if e := o.Send(ctx, Message{Group: true, Target: "g"}, "help"); e == nil {
		t.Fatal("403 ignored")
	}
	mu.Lock()
	if attempts != 6 {
		t.Fatal("retried denied send")
	}
	p := bodies[5]
	if _, ok := p["msg_id"]; ok {
		t.Fatal("proactive reused passive id")
	}
	deny = false
	mu.Unlock()
	if e := o.Send(ctx, Message{Target: "u", ID: "new"}, "query response"); e != nil {
		t.Fatal(e)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 7 || bodies[6]["content"] != "query response" {
		t.Fatal("old notification replayed")
	}
}
func TestWebhookSignatureAndChallenge(t *testing.T) {
	if validChallenge(`{"op":0}`, "123") || validChallenge("token", "not-a-timestamp") {
		t.Fatal("challenge signs arbitrary dispatch data")
	}
	c := testConfig()
	c.Official.AppID = "app"
	c.Official.AppSecret = "abc"
	o := NewOfficial(c)
	events := make(chan Message, 4)
	h := o.webhook(func(_ context.Context, m Message) { events <- m })
	key := deriveKey("abc")
	challenge := `{"op":13,"d":{"plain_token":"challenge","event_ts":"123"}}`
	req := httptest.NewRequest("POST", "/qq/webhook", strings.NewReader(challenge))
	req.Header.Set("X-Bot-Appid", "app")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var result map[string]string
	json.Unmarshal(rec.Body.Bytes(), &result)
	sig, _ := hex.DecodeString(result["signature"])
	if rec.Code != 200 || result["plain_token"] != "challenge" || !ed25519.Verify(key.Public().(ed25519.PublicKey), []byte("123challenge"), sig) {
		t.Fatal(rec.Body.String())
	}
	body := `{"op":0,"t":"GROUP_AT_MESSAGE_CREATE","d":{"id":"id","group_openid":"g","author":{"member_openid":"u","member_role":"admin"},"content":"/info"}}`
	stamp := strconv.FormatInt(time.Now().Unix(), 10)
	signature := hex.EncodeToString(ed25519.Sign(key, []byte(stamp+body)))
	for _, tc := range []struct {
		name, body, stamp, sig string
		code                   int
	}{{"valid", body, stamp, signature, 200}, {"tamper", body + " ", stamp, signature, 401}, {"wrong signature", body, stamp, "00", 401}, {"stale", body, "1", hex.EncodeToString(ed25519.Sign(key, []byte("1"+body))), 401}} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest("POST", "/qq/webhook", strings.NewReader(tc.body))
			r.Header.Set("X-Signature-Timestamp", tc.stamp)
			r.Header.Set("X-Signature-Ed25519", tc.sig)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
	select {
	case m := <-events:
		if m.Role != "admin" || m.Text != "/info" {
			t.Fatal(m)
		}
	case <-time.After(time.Second):
		t.Fatal("no dispatch")
	}
	select {
	case <-events:
		t.Fatal("invalid dispatch accepted")
	default:
	}
}
func TestSplitUTF8(t *testing.T) {
	p := splitText(strings.Repeat("字", 4000), 2500, 4)
	if len(p) != 4 {
		t.Fatal(len(p))
	}
	for _, s := range p {
		if !utf8.ValidString(s) {
			t.Fatal("split rune")
		}
	}
	if !strings.Contains(p[3], "截断") {
		t.Fatal("missing truncation marker")
	}
}

func TestOfficialGatewayIdentifyHeartbeatAndResume(t *testing.T) {
	identities := make(chan int, 2)
	serverErrors := make(chan error, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		ws.WriteJSON(map[string]any{"op": 10, "d": map[string]int{"heartbeat_interval": 1000}})
		var auth struct {
			OP int            `json:"op"`
			D  map[string]any `json:"d"`
		}
		if e = ws.ReadJSON(&auth); e != nil {
			serverErrors <- e
			return
		}
		identities <- auth.OP
		if auth.D["token"] != "QQBot test" {
			t.Error("gateway auth token")
		}
		if auth.OP == 6 {
			ws.WriteJSON(map[string]any{"op": 9})
			return
		}
		ws.WriteJSON(map[string]any{"op": 0, "s": 1, "t": "READY", "d": map[string]string{"session_id": "session"}})
		ws.WriteJSON(map[string]any{"op": 0, "s": 2, "t": "GROUP_AT_MESSAGE_CREATE", "d": map[string]any{"id": "m", "content": "/cx", "group_openid": "g", "author": map[string]string{"member_openid": "u", "member_role": "owner"}}})
		var heartbeat map[string]any
		ws.SetReadDeadline(time.Now().Add(3 * time.Second))
		if e = ws.ReadJSON(&heartbeat); e != nil {
			serverErrors <- e
			return
		}
		if heartbeat["op"] != float64(1) || heartbeat["d"] != float64(2) {
			t.Error(heartbeat)
		}
		ws.WriteJSON(map[string]int{"op": 11})
		ws.WriteJSON(map[string]int{"op": 7})
	}))
	defer srv.Close()
	o := NewOfficial(testConfig())
	o.token = "test"
	o.expires = time.Now().Add(time.Hour)
	events := make(chan Message, 1)
	session := ""
	for i := 0; i < 2; i++ {
		ws, _, e := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
		if e != nil {
			t.Fatal(e)
		}
		o.serveGateway(context.Background(), ws, &session, func(_ context.Context, m Message) { events <- m })
		if i == 0 && session != "session" {
			t.Fatal(session)
		}
	}
	if <-identities != 2 || <-identities != 6 || session != "" {
		t.Fatal("identify/resume invalid-session flow")
	}
	select {
	case e := <-serverErrors:
		t.Fatal(e)
	default:
	}
	select {
	case m := <-events:
		if m.Role != "owner" {
			t.Fatal(m)
		}
	default:
		t.Fatal("gateway dispatch")
	}
}
