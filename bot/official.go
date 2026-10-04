package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/gorilla/websocket"
)

type officialError struct{ status, code int }

func (e *officialError) Error() string {
	return fmt.Sprintf("官方 API 拒绝请求 HTTP=%d code=%d；检查票据、权限、群设置或频率限制（不会补发）", e.status, e.code)
}

type Official struct {
	c         Config
	http      *http.Client
	base      string
	tokenURL  string
	tokenMu   sync.Mutex
	token     string
	expires   time.Time
	seq       atomic.Int64
	sendMu    sync.Mutex
	msgSeq    map[string]int
	seqExpiry map[string]time.Time
}

func NewOfficial(c Config) *Official {
	base := "https://api.sgroup.qq.com"
	if c.Official.Sandbox {
		base = "https://sandbox.api.sgroup.qq.com"
	}
	return &Official{c: c, http: &http.Client{Timeout: 10 * time.Second}, base: base, tokenURL: "https://bots.qq.com/app/getAppAccessToken", msgSeq: map[string]int{}, seqExpiry: map[string]time.Time{}}
}
func (o *Official) getToken(ctx context.Context) (string, error) {
	o.tokenMu.Lock()
	defer o.tokenMu.Unlock()
	if o.token != "" && time.Now().Add(2*time.Minute).Before(o.expires) {
		return o.token, nil
	}
	body, _ := json.Marshal(map[string]string{"appId": o.c.Official.AppID, "clientSecret": o.c.Official.AppSecret})
	req, e := http.NewRequestWithContext(ctx, "POST", o.tokenURL, bytes.NewReader(body))
	if e != nil {
		return "", e
	}
	req.Header.Set("Content-Type", "application/json")
	resp, e := o.http.Do(req)
	if e != nil {
		return "", errors.New("官方访问凭证请求失败，请检查网络")
	}
	defer resp.Body.Close()
	var result struct {
		AccessToken string          `json:"access_token"`
		Expires     json.RawMessage `json:"expires_in"`
		Code        int             `json:"code"`
	}
	if json.NewDecoder(io.LimitReader(resp.Body, 65536)).Decode(&result) != nil {
		return "", errors.New("官方访问凭证返回格式异常")
	}
	if resp.StatusCode != 200 || result.AccessToken == "" {
		return "", &officialError{resp.StatusCode, result.Code}
	}
	n, _ := strconv.Atoi(rawID(result.Expires))
	if n <= 0 {
		n = 7200
	}
	o.token = result.AccessToken
	o.expires = time.Now().Add(time.Duration(n) * time.Second)
	log.Print("[官方] 访问凭证获取成功（凭据不输出到日志）")
	return o.token, nil
}
func (o *Official) request(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	token, e := o.getToken(ctx)
	if e != nil {
		return nil, e
	}
	var data []byte
	if body != nil {
		data, e = json.Marshal(body)
		if e != nil {
			return nil, e
		}
	}
	req, e := http.NewRequestWithContext(ctx, method, o.base+path, bytes.NewReader(data))
	if e != nil {
		return nil, e
	}
	req.Header.Set("Authorization", "QQBot "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Union-Appid", o.c.Official.AppID)
	resp, e := o.http.Do(req)
	if e != nil {
		return nil, errors.New("官方 API 网络请求失败；发送结果未知，不自动重发")
	}
	defer resp.Body.Close()
	result, e := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if e != nil {
		return nil, errors.New("官方 API 响应读取失败")
	}
	var code struct {
		Code int `json:"code"`
	}
	_ = json.Unmarshal(result, &code)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || code.Code != 0 {
		if resp.StatusCode == 401 {
			o.tokenMu.Lock()
			o.token = ""
			o.tokenMu.Unlock()
		}
		return nil, &officialError{resp.StatusCode, code.Code}
	}
	return result, nil
}
func (o *Official) Run(ctx context.Context, handler func(context.Context, Message)) error {
	if _, e := o.getToken(ctx); e != nil {
		return e
	}
	if o.c.Official.Transport == "webhook" {
		return o.runWebhook(ctx, handler)
	}
	session := ""
	delay := time.Second
	for ctx.Err() == nil {
		data, e := o.request(ctx, "GET", "/gateway", nil)
		if e == nil {
			var gateway struct {
				URL string `json:"url"`
			}
			_ = json.Unmarshal(data, &gateway)
			if gateway.URL == "" {
				return errors.New("官方未提供 WebSocket 网关；请用重新配置脚本切换 Webhook 并配置公网 HTTPS 回调")
			}
			var conn *websocket.Conn
			conn, _, e = (&websocket.Dialer{HandshakeTimeout: 10 * time.Second}).DialContext(ctx, gateway.URL, nil)
			if e == nil {
				log.Print("[官方] 已连接网关，等待鉴权")
				e = o.serveGateway(ctx, conn, &session, handler)
			}
		}
		if ctx.Err() != nil {
			break
		}
		var apiError *officialError
		if errors.As(e, &apiError) && (apiError.status == 401 || apiError.status == 403) {
			return errors.New("官方鉴权或网关权限被拒绝；核对 AppID/AppSecret、平台 IP 白名单；账号不支持 WebSocket 时配置 Webhook")
		}
		log.Printf("[官方] 网关连接中断，%s 后重试；持续失败请核对平台接收方式并配置 Webhook", delay)
		if !waitContext(ctx, delay) {
			break
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
	return nil
}
func (o *Official) serveGateway(ctx context.Context, conn *websocket.Conn, session *string, handler func(context.Context, Message)) error {
	defer conn.Close()
	conn.SetReadLimit(2 * 1024 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
	var writeMu sync.Mutex
	send := func(v any) error {
		writeMu.Lock()
		defer writeMu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		return conn.WriteJSON(v)
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			_ = conn.Close()
		case <-stop:
		}
	}()
	started := false
	var ack atomic.Bool
	for {
		var p struct {
			OP       int             `json:"op"`
			Data     json.RawMessage `json:"d"`
			Sequence *int64          `json:"s"`
			Type     string          `json:"t"`
		}
		if e := conn.ReadJSON(&p); e != nil {
			return e
		}
		if p.Sequence != nil {
			o.seq.Store(*p.Sequence)
		}
		switch p.OP {
		case 10:
			if started {
				return errors.New("重复网关 Hello")
			}
			started = true
			var hello struct {
				Interval int `json:"heartbeat_interval"`
			}
			if json.Unmarshal(p.Data, &hello) != nil || hello.Interval < 1000 || hello.Interval > 120000 {
				return errors.New("无效心跳间隔")
			}
			interval := time.Duration(hello.Interval) * time.Millisecond
			_ = conn.SetReadDeadline(time.Now().Add(3 * interval))
			token, e := o.getToken(ctx)
			if e != nil {
				return e
			}
			if *session != "" {
				e = send(map[string]any{"op": 6, "d": map[string]any{"token": "QQBot " + token, "session_id": *session, "seq": o.seq.Load()}})
			} else {
				e = send(map[string]any{"op": 2, "d": map[string]any{"token": "QQBot " + token, "intents": 1 << 25, "shard": []int{0, 1}}})
			}
			if e != nil {
				return e
			}
			ack.Store(true)
			go func() {
				t := time.NewTicker(interval)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case <-t.C:
						if !ack.Swap(false) {
							_ = conn.Close()
							return
						}
						if send(map[string]any{"op": 1, "d": o.seq.Load()}) != nil {
							_ = conn.Close()
							return
						}
					}
				}
			}()
		case 11:
			ack.Store(true)
			_ = conn.SetReadDeadline(time.Now().Add(4 * time.Minute))
		case 1:
			if e := send(map[string]any{"op": 1, "d": o.seq.Load()}); e != nil {
				return e
			}
		case 7:
			return errors.New("平台要求重新连接")
		case 9:
			*session = ""
			o.seq.Store(0)
			return errors.New("会话失效，重新鉴权")
		case 0:
			if p.Type == "READY" {
				var ready struct {
					Session string `json:"session_id"`
				}
				_ = json.Unmarshal(p.Data, &ready)
				*session = ready.Session
				log.Print("[官方] 机器人已就绪")
			}
			if p.Type == "RESUMED" {
				log.Print("[官方] 会话已恢复")
			}
			if m, ok := officialMessage(p.Type, p.Data); ok {
				go handler(ctx, m)
			}
		}
	}
}
func officialMessage(kind string, data json.RawMessage) (Message, bool) {
	if kind != "GROUP_AT_MESSAGE_CREATE" && kind != "GROUP_MESSAGE_CREATE" && kind != "C2C_MESSAGE_CREATE" {
		return Message{}, false
	}
	var ev struct {
		ID      string `json:"id"`
		Content string `json:"content"`
		GroupID string `json:"group_openid"`
		UserID  string `json:"user_openid"`
		Author  struct {
			ID       string `json:"id"`
			MemberID string `json:"member_openid"`
			UserID   string `json:"user_openid"`
			Role     string `json:"member_role"`
			Bot      bool   `json:"bot"`
		} `json:"author"`
	}
	if json.Unmarshal(data, &ev) != nil || ev.Author.Bot {
		return Message{}, false
	}
	m := Message{ID: ev.ID, Text: strings.TrimSpace(ev.Content), Role: ev.Author.Role, Group: kind != "C2C_MESSAGE_CREATE"}
	if m.Group {
		m.Target = ev.GroupID
		m.Sender = ev.Author.MemberID
	} else {
		m.Target = ev.UserID
		if m.Target == "" {
			m.Target = ev.Author.UserID
		}
		m.Sender = m.Target
	}
	if m.Sender == "" {
		m.Sender = ev.Author.ID
	}
	if m.Target == "" && !m.Group {
		m.Target = m.Sender
	}
	return m, m.ID != "" && m.Target != "" && m.Sender != ""
}
func deriveKey(secret string) ed25519.PrivateKey {
	seed := []byte(secret)
	if len(seed) == 0 {
		return nil
	}
	for len(seed) < ed25519.SeedSize {
		seed = append(seed, seed...)
	}
	return ed25519.NewKeyFromSeed(seed[:ed25519.SeedSize])
}
func verifyCallback(key ed25519.PrivateKey, stamp, signature string, body []byte) bool {
	if key == nil {
		return false
	}
	sig, e := hex.DecodeString(signature)
	if e != nil {
		return false
	}
	ts, e := strconv.ParseInt(stamp, 10, 64)
	if e != nil || time.Now().Unix()-ts > 300 || ts-time.Now().Unix() > 300 {
		return false
	}
	data := append([]byte(stamp), body...)
	return ed25519.Verify(key.Public().(ed25519.PublicKey), data, sig)
}
func validChallenge(token, timestamp string) bool {
	// Challenge nonces must not turn this endpoint into a signer for arbitrary dispatch JSON.
	if token == "" || len(token) > 4096 {
		return false
	}
	if n, e := strconv.ParseInt(timestamp, 10, 64); e != nil || n <= 0 {
		return false
	}
	for _, r := range token {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || strings.ContainsRune("-_+/=", r)) {
			return false
		}
	}
	return true
}
func (o *Official) webhook(handler func(context.Context, Message)) http.Handler {
	key := deriveKey(o.c.Official.AppSecret)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "method not allowed", 405)
			return
		}
		body, e := io.ReadAll(http.MaxBytesReader(w, r.Body, 2*1024*1024))
		if e != nil {
			http.Error(w, "invalid body", 400)
			return
		}
		var p struct {
			OP   int             `json:"op"`
			Data json.RawMessage `json:"d"`
			Type string          `json:"t"`
		}
		if json.Unmarshal(body, &p) != nil {
			http.Error(w, "invalid JSON", 400)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if p.OP == 13 {
			// Platform validation is a challenge, not a dispatch event, and has no signature headers.
			if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Bot-Appid")), []byte(o.c.Official.AppID)) != 1 {
				http.Error(w, "wrong app id", 403)
				return
			}
			var challenge struct {
				Token     string `json:"plain_token"`
				Timestamp string `json:"event_ts"`
			}
			if json.Unmarshal(p.Data, &challenge) != nil || !validChallenge(challenge.Token, challenge.Timestamp) {
				http.Error(w, "invalid challenge", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"plain_token": challenge.Token, "signature": hex.EncodeToString(ed25519.Sign(key, []byte(challenge.Timestamp+challenge.Token)))})
			log.Print("[官方] Webhook 回调地址验证已响应")
			return
		}
		if !verifyCallback(key, r.Header.Get("X-Signature-Timestamp"), r.Header.Get("X-Signature-Ed25519"), body) {
			http.Error(w, "invalid signature", 401)
			return
		}
		if m, ok := officialMessage(p.Type, p.Data); ok {
			go handler(context.Background(), m)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"op": 12, "d": map[string]int{"code": 0}})
	})
}
func (o *Official) runWebhook(ctx context.Context, handler func(context.Context, Message)) error {
	mux := http.NewServeMux()
	mux.Handle(o.c.Official.Path, o.webhook(handler))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	l, e := net.Listen("tcp", o.c.Official.Listen)
	if e != nil {
		return fmt.Errorf("Webhook 监听失败：%w", e)
	}
	defer l.Close()
	log.Printf("[官方] Webhook 已监听 %s%s，等待平台验证（不等于已接入）", o.c.Official.Listen, o.c.Official.Path)
	go func() {
		<-ctx.Done()
		stop, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(stop)
	}()
	e = srv.Serve(l)
	if errors.Is(e, http.ErrServerClosed) {
		return nil
	}
	return e
}
func splitText(text string, limit, max int) []string {
	parts := []string{}
	for len(text) > 0 && len(parts) < max {
		n := len(text)
		if n > limit {
			n = limit
			for n > 0 && !utf8.RuneStart(text[n]) {
				n--
			}
		}
		parts = append(parts, text[:n])
		text = text[n:]
	}
	if text != "" && len(parts) > 0 {
		parts[len(parts)-1] += "\n（内容过长，已截断）"
	}
	return parts
}
func (o *Official) nextSequence(m Message) (int, error) {
	o.sendMu.Lock()
	defer o.sendMu.Unlock()
	now := time.Now()
	for k, t := range o.seqExpiry {
		if now.After(t) {
			delete(o.seqExpiry, k)
			delete(o.msgSeq, k)
		}
	}
	key := fmt.Sprintf("%t:%s:%s", m.Group, m.Target, m.ID)
	max := 4
	if m.Group {
		max = 5
	}
	if o.msgSeq[key] >= max {
		return 0, errors.New("此消息的官方被动回复次数已用完，跳过发送（不补发）")
	}
	o.msgSeq[key]++
	o.seqExpiry[key] = now.Add(time.Hour)
	return o.msgSeq[key], nil
}
func (o *Official) messagePath(m Message) string {
	if m.Group {
		return "/v2/groups/" + url.PathEscape(m.Target) + "/messages"
	}
	return "/v2/users/" + url.PathEscape(m.Target) + "/messages"
}
func (o *Official) Send(ctx context.Context, m Message, text string) error {
	max := 4
	if m.Group {
		max = 5
	}
	if m.ID == "" {
		max = 1
	}
	for _, part := range splitText(text, 2500, max) {
		body := map[string]any{"msg_type": 0, "content": part}
		if m.ID != "" {
			seq, e := o.nextSequence(m)
			if e != nil {
				return e
			}
			body["msg_id"] = m.ID
			body["msg_seq"] = seq
		}
		if _, e := o.request(ctx, "POST", o.messagePath(m), body); e != nil {
			return e
		}
	}
	return nil
}
func (o *Official) Image(ctx context.Context, m Message, path string) error {
	b, e := os.ReadFile(path)
	if e != nil {
		return errors.New("图片读取失败")
	}
	if len(b) > 8*1024*1024 {
		return errors.New("探针图片超过 8 MiB")
	}
	seq := 0
	if m.ID != "" {
		seq, e = o.nextSequence(m)
		if e != nil {
			return e
		}
	}
	endpoint := strings.TrimSuffix(o.messagePath(m), "/messages") + "/files"
	data, e := o.request(ctx, "POST", endpoint, map[string]any{"file_type": 1, "file_data": base64.StdEncoding.EncodeToString(b), "srv_send_msg": false})
	if e != nil {
		return e
	}
	var uploaded struct {
		Info string `json:"file_info"`
	}
	if json.Unmarshal(data, &uploaded) != nil || uploaded.Info == "" {
		return errors.New("官方图片上传没有返回 file_info")
	}
	body := map[string]any{"msg_type": 7, "media": map[string]string{"file_info": uploaded.Info}}
	if m.ID != "" {
		body["msg_id"] = m.ID
		body["msg_seq"] = seq
	}
	_, e = o.request(ctx, "POST", o.messagePath(m), body)
	return e
}
