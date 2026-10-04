package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Message struct {
	Group  bool
	Target string
	Sender string
	Role   string
	ID     string
	Text   string
}
type Adapter interface {
	Run(context.Context, func(context.Context, Message)) error
	Send(context.Context, Message, string) error
	Image(context.Context, Message, string) error
}
type Bot struct {
	c       Config
	bridge  *Bridge
	adapter Adapter
	dir     string
	mu      sync.Mutex
	seen    map[string]time.Time
	work    chan struct{}
}

func NewBot(c Config, b *Bridge, a Adapter, dir string) *Bot {
	return &Bot{c: c, bridge: b, adapter: a, dir: dir, seen: map[string]time.Time{}, work: make(chan struct{}, 16)}
}
func includes(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
func (b *Bot) first(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	for k, t := range b.seen {
		if now.Sub(t) > 10*time.Minute {
			delete(b.seen, k)
		}
	}
	if _, ok := b.seen[key]; ok {
		return false
	}
	if len(b.seen) >= 10000 {
		return false
	}
	b.seen[key] = now
	return true
}
func (b *Bot) authorized(m Message) bool {
	if len(b.c.AdministratorIDs) > 0 {
		return includes(b.c.AdministratorIDs, m.Sender)
	}
	return m.Group && (m.Role == "admin" || m.Role == "owner")
}
func (b *Bot) reply(ctx context.Context, m Message, text string) bool {
	if e := b.adapter.Send(ctx, m, text); e != nil {
		log.Printf("[QQ] 回复失败：%v", e)
		return false
	}
	return true
}
func (b *Bot) Message(ctx context.Context, m Message) {
	if m.Sender == "" || m.Target == "" || m.ID == "" {
		return
	}
	if m.Group && len(b.c.AllowedGroups) > 0 && !includes(b.c.AllowedGroups, m.Target) {
		return
	}
	if !b.first(fmt.Sprintf("msg:%t:%s:%s", m.Group, m.Target, m.ID)) {
		return
	}
	select {
	case b.work <- struct{}{}:
	default:
		log.Print("[QQ] 消息处理繁忙，忽略新请求")
		return
	}
	defer func() { <-b.work }()
	t := strings.TrimSpace(m.Text)
	// IDs are useful for official OpenID configuration; credentials and chat content are never logged.
	log.Printf("[QQ] 会话=%s 用户=%s 角色=%s", m.Target, m.Sender, m.Role)
	requestCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	switch {
	case t == "/cx":
		b.reply(requestCtx, m, b.queryAll(requestCtx, "cx"))
	case t == "/info":
		b.reply(requestCtx, m, b.queryAll(requestCtx, "info"))
	case strings.HasPrefix(t, "/列表") || strings.HasPrefix(t, "/＃"):
		f := strings.Fields(t)
		if (f[0] != "/列表" && f[0] != "/＃") || len(f) > 2 {
			return
		}
		i := 1
		if len(f) == 2 {
			n, e := strconv.Atoi(f[1])
			if e != nil {
				b.reply(requestCtx, m, "服务器序号必须是整数。")
				return
			}
			i = n
		}
		b.reply(requestCtx, m, b.list(requestCtx, i))
	case strings.Contains(t, "/服务器状态") || strings.Contains(t, "炸了？") || strings.Contains(t, "炸了?"):
		if !b.reply(requestCtx, m, b.queryAll(requestCtx, "cx")) {
			return
		}
		if b.c.StatusImage != "" {
			path := b.c.StatusImage
			if !filepath.IsAbs(path) {
				path = filepath.Join(b.dir, path)
			}
			if _, e := os.Stat(path); e != nil {
				log.Print("[图片] 探针图片不存在，已发送文字状态")
			} else if e = b.adapter.Image(requestCtx, m, path); e != nil {
				log.Printf("[图片] 发送失败：%v", e)
			}
		}
	case strings.Contains(t, "helloworld"):
		b.reply(requestCtx, m, "hello world！")
	case t == "round" || t == "/round" || strings.HasPrefix(t, "round ") || strings.HasPrefix(t, "/round "):
		if !b.authorized(m) {
			b.reply(requestCtx, m, "无管理权限。管理员名单非空时按名单授权；留空时仅当前群管理员与群主可用。未提供群身份或私聊需要配置管理员 ID。")
			return
		}
		b.reply(requestCtx, m, b.round(requestCtx, t))
	}
}
func (b *Bot) call(ctx context.Context, index int, method string, params any) (GameResult, error) {
	if index < 1 || index > len(b.c.Servers) {
		return GameResult{}, fmt.Errorf("服务器序号有效范围为 1-%d", len(b.c.Servers))
	}
	c, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return b.bridge.Call(c, b.c.Servers[index-1].ID, method, params)
}
func (b *Bot) list(ctx context.Context, index int) string {
	r, e := b.call(ctx, index, "list", struct{}{})
	if e != nil {
		return e.Error()
	}
	name := b.c.Servers[index-1].Name
	var out strings.Builder
	fmt.Fprintf(&out, "%s 玩家列表\n", name)
	if len(r.Players) == 0 {
		out.WriteString("当前空无一人。")
	}
	for _, p := range r.Players {
		fmt.Fprintf(&out, "%s-%d\n", p.Name, p.ID)
	}
	return out.String()
}
func (b *Bot) queryAll(ctx context.Context, method string) string {
	type answer struct {
		result GameResult
		err    error
	}
	results := make([]answer, len(b.c.Servers))
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) { defer wg.Done(); results[i].result, results[i].err = b.call(ctx, i+1, method, struct{}{}) }(i)
	}
	wg.Wait()
	var out strings.Builder
	total := 0
	online := 0
	for i, a := range results {
		name := b.c.Servers[i].Name
		if a.err != nil {
			fmt.Fprintf(&out, "#%d %s：%s\n", i+1, name, a.err)
			continue
		}
		online++
		r := a.result
		total += r.Online
		fmt.Fprintf(&out, "#%d %s\n在线人数：%d/%d\n在线管理：%d人\n", i+1, name, r.Online, r.Max, r.Admins)
		if method == "info" {
			refresh := "暂不可用"
			if r.RespawnSeconds != nil {
				refresh = fmt.Sprintf("%d秒", *r.RespawnSeconds)
			}
			fmt.Fprintf(&out, "DD人数：%d\n博士人数：%d\nSCP人数：%d\n回合时间：%s\n回合次数：%d\n下一波刷新：%s\n", r.ClassD, r.Scientists, r.SCPs, (time.Duration(r.RoundSeconds) * time.Second).String(), r.RoundCount, refresh)
		}
	}
	if method == "cx" {
		fmt.Fprintf(&out, "总在线人数：%d\n", total)
		if online > 0 && total == 0 {
			out.WriteString("在线服务器当前空无一人，你还不快去暖服！\n")
		}
	}
	fmt.Fprintf(&out, "查询时间：%s", time.Now().Format("2006-01-02 15:04:05"))
	return out.String()
}
func parseRound(text string, count int) (int, string, map[string]any, error) {
	f := strings.Fields(text)
	if len(f) < 2 {
		return 0, "", nil, fmt.Errorf("用法：round [服务器序号] list/start/rest/allrest/kick+玩家ID/bc+内容")
	}
	index := 1
	at := 1
	if n, e := strconv.Atoi(f[1]); e == nil {
		index = n
		at++
	}
	if index < 1 || index > count {
		return 0, "", nil, fmt.Errorf("服务器序号有效范围为 1-%d", count)
	}
	if len(f) <= at {
		return 0, "", nil, fmt.Errorf("缺少操作")
	}
	op := strings.Join(f[at:], " ")
	args := map[string]any{}
	if strings.HasPrefix(op, "kick+") {
		n, e := strconv.Atoi(strings.TrimPrefix(op, "kick+"))
		if e != nil || n < 1 {
			return 0, "", nil, fmt.Errorf("用法：round [序号] kick+玩家ID")
		}
		args["player_id"] = n
		return index, "kick", args, nil
	}
	if strings.HasPrefix(op, "bc+") {
		s := strings.TrimSpace(strings.TrimPrefix(op, "bc+"))
		if s == "" || len([]rune(s)) > 500 {
			return 0, "", nil, fmt.Errorf("广播内容长度必须为 1-500 字")
		}
		args["text"] = s
		return index, "bc", args, nil
	}
	if op != "list" && op != "start" && op != "rest" && op != "allrest" {
		return 0, "", nil, fmt.Errorf("未知 round 操作")
	}
	return index, op, args, nil
}
func (b *Bot) round(ctx context.Context, text string) string {
	i, method, args, e := parseRound(text, len(b.c.Servers))
	if e != nil {
		return e.Error()
	}
	if method == "list" {
		return b.list(ctx, i)
	}
	r, e := b.call(ctx, i, method, args)
	if e != nil {
		return e.Error()
	}
	return b.c.Servers[i-1].Name + "：" + r.Message
}
func (b *Bot) Help(serverID string, ev HelpEvent) {
	if !b.first("help:" + serverID + ":" + ev.ID) {
		return
	}
	s := b.bridge.servers[serverID]
	log.Printf("[求助] %s 收到玩家求助，通知目标：群%d / 私聊%d", s.Name, len(b.c.AlertGroups), len(b.c.AlertUsers))
	select {
	case b.work <- struct{}{}:
	default:
		log.Print("[求助] 处理繁忙，跳过通知")
		return
	}
	go func() {
		defer func() { <-b.work }()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		text := fmt.Sprintf("来自服务器：%s\n[管理员求助]\n玩家：%s\n求助：%s", s.Name, ev.Player, ev.Text)
		for _, g := range b.c.AlertGroups {
			if len(b.c.AllowedGroups) > 0 && !includes(b.c.AllowedGroups, g) {
				continue
			}
			if e := b.adapter.Send(ctx, Message{Group: true, Target: g}, text); e != nil {
				log.Printf("[求助] 群通知未发送：%v（不缓存或补发）", e)
			}
		}
		for _, u := range b.c.AlertUsers {
			if e := b.adapter.Send(ctx, Message{Target: u}, text); e != nil {
				log.Printf("[求助] 私聊通知未发送：%v（不缓存或补发）", e)
			}
		}
	}()
}
