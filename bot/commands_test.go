package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordedSend struct {
	m    Message
	text string
}
type fakeAdapter struct {
	mu      sync.Mutex
	sends   []recordedSend
	images  int
	failure error
	notify  chan struct{}
}

func (a *fakeAdapter) Run(context.Context, func(context.Context, Message)) error { return nil }
func (a *fakeAdapter) Send(_ context.Context, m Message, s string) error {
	a.mu.Lock()
	a.sends = append(a.sends, recordedSend{m, s})
	a.mu.Unlock()
	if a.notify != nil {
		a.notify <- struct{}{}
	}
	return a.failure
}
func (a *fakeAdapter) Image(context.Context, Message, string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.images++
	return a.failure
}
func (a *fakeAdapter) snapshot() ([]recordedSend, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]recordedSend(nil), a.sends...), a.images
}
func testConfig() Config {
	c := defaults()
	c.Servers = []ServerConfig{{"31140", "1服", strings.Repeat("a", 64)}, {"31141", "2服", strings.Repeat("b", 64)}}
	return c
}

func TestAuthorization(t *testing.T) {
	for _, tc := range []struct {
		name string
		ids  []string
		m    Message
		want bool
	}{
		{"empty admin", nil, Message{Group: true, Sender: "u", Role: "admin"}, true},
		{"empty owner", nil, Message{Group: true, Sender: "u", Role: "owner"}, true},
		{"member", nil, Message{Group: true, Sender: "u", Role: "member"}, false},
		{"unknown", nil, Message{Group: true, Sender: "u"}, false},
		{"private role cannot grant", nil, Message{Sender: "u", Role: "owner"}, false},
		{"explicit overrides owner", []string{"other"}, Message{Group: true, Sender: "u", Role: "owner"}, false},
		{"explicit private", []string{"u"}, Message{Sender: "u"}, true},
		{"explicit ordinary", []string{"u"}, Message{Group: true, Sender: "u", Role: "member"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := testConfig()
			c.AdministratorIDs = tc.ids
			b := NewBot(c, NewBridge(c.Servers), &fakeAdapter{}, "")
			if b.authorized(tc.m) != tc.want {
				t.Fatal("unexpected authorization")
			}
		})
	}
}
func TestRoundSyntax(t *testing.T) {
	for _, tc := range []struct {
		text   string
		index  int
		method string
		arg    any
	}{
		{"round list", 1, "list", nil}, {"/round start", 1, "start", nil}, {"round 2 rest", 2, "rest", nil},
		{"round allrest", 1, "allrest", nil}, {"round 2 kick+17", 2, "kick", 17}, {"round bc+大家好 欢迎", 1, "bc", "大家好 欢迎"},
	} {
		t.Run(tc.text, func(t *testing.T) {
			i, m, p, e := parseRound(tc.text, 2)
			if e != nil || i != tc.index || m != tc.method {
				t.Fatalf("%d %s %v", i, m, e)
			}
			if tc.method == "kick" && p["player_id"] != tc.arg {
				t.Fatal(p)
			}
			if tc.method == "bc" && p["text"] != tc.arg {
				t.Fatal(p)
			}
		})
	}
	for _, s := range []string{"round", "round 0 start", "round 3 start", "round 2", "round kick+0", "round kick+abc", "round ban+2", "round bc+", "round bc+" + strings.Repeat("字", 501), "round start extra"} {
		if _, _, _, e := parseRound(s, 2); e == nil {
			t.Errorf("accepted %s", s)
		}
	}
}
func TestRoutingAndRemovedCommands(t *testing.T) {
	c := testConfig()
	a := &fakeAdapter{}
	b := NewBot(c, NewBridge(c.Servers), a, "")
	send := func(id, text string) {
		b.Message(context.Background(), Message{Group: true, Target: "g", Sender: "u", Role: "member", ID: id, Text: text})
	}
	for i, s := range []string{"/绑定 123", "/击杀榜", "/查询击杀信息", "/帮助", "/ping", "/复读 hi", "/列表x", "random"} {
		send(fmt.Sprint(i), s)
	}
	if s, _ := a.snapshot(); len(s) != 0 {
		t.Fatal(s)
	}
	send("cx", "/cx")
	send("cx", "/cx")
	send("info", "/info")
	send("l", "/＃")
	send("hello", "helloworld")
	send("denied", "round start")
	s, _ := a.snapshot()
	if len(s) != 5 {
		t.Fatal(len(s))
	}
	if !strings.Contains(s[0].text, "服务器未连接") || strings.Contains(s[0].text, "暖服") {
		t.Fatal(s[0].text)
	}
	if !strings.Contains(s[4].text, "无管理权限") {
		t.Fatal(s[4].text)
	}
	c.AllowedGroups = []string{"allowed"}
	a2 := &fakeAdapter{}
	b2 := NewBot(c, NewBridge(c.Servers), a2, "")
	b2.Message(context.Background(), Message{Group: true, Target: "other", Sender: "u", ID: "id", Text: "helloworld"})
	if s, _ := a2.snapshot(); len(s) != 0 {
		t.Fatal("group filter")
	}
}
func TestDeniedTextSkipsImage(t *testing.T) {
	c := testConfig()
	c.StatusImage = "existing.png"
	a := &fakeAdapter{failure: errors.New("forbidden")}
	b := NewBot(c, NewBridge(c.Servers), a, t.TempDir())
	b.Message(context.Background(), Message{Target: "u", Sender: "u", ID: "status", Text: "炸了？"})
	s, images := a.snapshot()
	if len(s) != 1 || images != 0 {
		t.Fatal("must stop after rejected text")
	}
}
func TestHelpNotCachedOrReplayed(t *testing.T) {
	c := testConfig()
	c.AlertGroups = []string{"g"}
	a := &fakeAdapter{failure: errors.New("permission denied"), notify: make(chan struct{}, 4)}
	b := NewBot(c, NewBridge(c.Servers), a, "")
	ev := HelpEvent{"e", "玩家", "请处理"}
	b.Help("31140", ev)
	select {
	case <-a.notify:
	case <-time.After(time.Second):
		t.Fatal("missing single attempt")
	}
	b.Help("31140", ev)
	b.Message(context.Background(), Message{Group: true, Target: "g", Sender: "u", ID: "later", Text: "/cx"})
	s, _ := a.snapshot()
	if len(s) != 2 || strings.Contains(s[1].text, "请处理") {
		t.Fatal(s)
	}
}
