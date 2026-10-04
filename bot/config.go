package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ServerConfig struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Token string `json:"token"`
}
type Config struct {
	Adapter string `json:"adapter"`
	NapCat  struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	} `json:"napcat"`
	Official struct {
		AppID     string `json:"app_id"`
		AppSecret string `json:"app_secret"`
		Transport string `json:"transport"`
		Sandbox   bool   `json:"sandbox"`
		Listen    string `json:"listen"`
		Path      string `json:"path"`
	} `json:"official"`
	BridgeListen     string         `json:"bridge_listen"`
	Servers          []ServerConfig `json:"servers"`
	AllowedGroups    []string       `json:"allowed_groups"`
	AdministratorIDs []string       `json:"administrator_ids"`
	AlertGroups      []string       `json:"alert_groups"`
	AlertUsers       []string       `json:"alert_users"`
	StatusImage      string         `json:"status_image"`
}

func defaults() Config {
	c := Config{Adapter: "napcat", BridgeListen: "127.0.0.1:17890"}
	c.NapCat.URL = "ws://127.0.0.1:3001"
	c.Official.Transport = "websocket"
	c.Official.Listen = "127.0.0.1:8080"
	c.Official.Path = "/qq/webhook"
	c.Servers = []ServerConfig{}
	c.AllowedGroups = []string{}
	c.AdministratorIDs = []string{}
	c.AlertGroups = []string{}
	c.AlertUsers = []string{}
	return c
}
func validate(c Config) error {
	if _, port, e := net.SplitHostPort(c.BridgeListen); e != nil || !validPort(port) {
		return errors.New("bridge_listen 必须为 主机:端口，例如 127.0.0.1:17890")
	}
	if c.Adapter != "napcat" && c.Adapter != "official" {
		return errors.New("adapter 必须为 napcat 或 official")
	}
	if c.Adapter == "napcat" {
		u, e := url.Parse(c.NapCat.URL)
		if e != nil || u.Host == "" || (u.Scheme != "ws" && u.Scheme != "wss") {
			return errors.New("napcat.url 必须为有效的 ws:// 或 wss:// 地址")
		}
	} else {
		if strings.TrimSpace(c.Official.AppID) == "" || strings.TrimSpace(c.Official.AppSecret) == "" {
			return errors.New("请填写 official.app_id 与 official.app_secret")
		}
		if c.Official.Transport != "websocket" && c.Official.Transport != "webhook" {
			return errors.New("official.transport 必须为 websocket 或 webhook")
		}
		if c.Official.Path == "" || !strings.HasPrefix(c.Official.Path, "/") {
			return errors.New("official.path 必须以 / 开头")
		}
		if c.Official.Transport == "webhook" {
			if _, port, e := net.SplitHostPort(c.Official.Listen); e != nil || !validPort(port) {
				return errors.New("official.listen 必须为 主机:端口")
			}
		}
	}
	if len(c.Servers) == 0 {
		return errors.New("请至少配置一个 servers 条目")
	}
	ids := map[string]bool{}
	for _, s := range c.Servers {
		if s.ID == "" || s.Name == "" || len(s.Token) < 32 || strings.HasPrefix(s.Token, "REPLACE_") {
			return errors.New("服务器必须填写 id、name 和至少 32 字符的 token")
		}
		if ids[s.ID] {
			return fmt.Errorf("服务器 id 重复：%s", s.ID)
		}
		ids[s.ID] = true
	}
	if c.Adapter == "napcat" {
		for _, list := range [][]string{c.AllowedGroups, c.AdministratorIDs, c.AlertGroups, c.AlertUsers} {
			for _, id := range list {
				if len(id) == 0 || strings.Trim(id, "0123456789") != "" || strings.Trim(id, "0") == "" {
					return errors.New("NapCat 的群 ID 与用户 ID 必须是数字 QQ 号码；官方模式使用 OpenID")
				}
			}
		}
	}
	return nil
}
func validPort(port string) bool { n, e := strconv.Atoi(port); return e == nil && n > 0 && n <= 65535 }
func loadConfig(path string) (Config, error) {
	c := defaults()
	b, e := os.ReadFile(path)
	if e != nil {
		return c, e
	}
	b = bytes.TrimPrefix(b, []byte{0xef, 0xbb, 0xbf})
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if e = dec.Decode(&c); e != nil {
		return c, fmt.Errorf("配置解析失败（原文件未修改）：%w", e)
	}
	if !json.Valid(b) {
		return c, errors.New("配置包含多余内容或无效 JSON，原文件未修改")
	}
	return c, validate(c)
}
func saveJSON(path string, value any) error {
	b, e := json.MarshalIndent(value, "", "  ")
	if e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		return e
	}
	if e = os.WriteFile(path+".tmp", append(b, '\n'), 0600); e != nil {
		return e
	}
	return os.Rename(path+".tmp", path)
}
func splitIDs(s string) []string {
	out := []string{}
	for _, v := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '，' || r == '*' || r == ' ' }) {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}
func setup(path string) (Config, error) {
	c := defaults()
	if old, e := loadConfig(path); e == nil {
		c = old
	}
	r := bufio.NewReader(os.Stdin)
	ask := func(label, def string) string {
		fmt.Printf("%s [%s]：", label, def)
		s, e := r.ReadString('\n')
		if e != nil && len(s) == 0 {
			return ""
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return def
		}
		if s == "-" {
			return ""
		}
		return s
	}
	fmt.Println("\nQQBotLite — 首次配置 / 重新配置（Ctrl+C 取消）")
	fmt.Println("回车保留方括号内的值；输入 - 可清空可选项。")
	mode := ask("接入方式：1=NapCat，2=QQ 官方 API", map[bool]string{true: "2", false: "1"}[c.Adapter == "official"])
	if mode == "2" {
		c.Adapter = "official"
		c.Official.AppID = ask("AppID", c.Official.AppID)
		fmt.Print("AppSecret（保存至本机配置；不会写入日志）：")
		secret, _ := r.ReadString('\n')
		secret = strings.TrimSpace(secret)
		if secret != "" {
			c.Official.AppSecret = secret
		}
		transport := ask("接收方式：1=WebSocket，2=Webhook", map[bool]string{true: "2", false: "1"}[c.Official.Transport == "webhook"])
		if transport == "2" {
			c.Official.Transport = "webhook"
			c.Official.Listen = ask("Webhook 本地监听", c.Official.Listen)
			fmt.Println("需在 QQ 平台配置公网 HTTPS 回调，反代到本地监听；具体见使用说明。")
		} else {
			c.Official.Transport = "websocket"
		}
		c.Official.Sandbox = strings.EqualFold(ask("使用沙箱？y/n", "n"), "y")
	} else if mode == "1" {
		c.Adapter = "napcat"
		c.NapCat.URL = ask("NapCat WebSocket 服务端地址", c.NapCat.URL)
		c.NapCat.Token = ask("NapCat Token（未设置可留空）", c.NapCat.Token)
	} else {
		return c, errors.New("接入方式请输入 1 或 2")
	}
	c.BridgeListen = ask("游戏插件连接的本地监听地址", c.BridgeListen)
	previous := []string{}
	tokens := map[string]string{}
	names := map[string]string{}
	for _, s := range c.Servers {
		previous = append(previous, s.ID)
		tokens[s.ID] = s.Token
		names[s.ID] = s.Name
	}
	portDefault := strings.Join(previous, ",")
	if portDefault == "" {
		portDefault = "31140"
	}
	ports := splitIDs(ask("游戏服端口（逗号分隔，顺序即服务器序号）", portDefault))
	c.Servers = nil
	for i, p := range ports {
		n, e := strconv.Atoi(p)
		if e != nil || n < 1 || n > 65535 {
			return c, fmt.Errorf("无效游戏端口：%s", p)
		}
		t := tokens[p]
		if t == "" {
			b := make([]byte, 32)
			if _, e = rand.Read(b); e != nil {
				return c, e
			}
			t = hex.EncodeToString(b)
		}
		name := names[p]
		if name == "" {
			name = fmt.Sprintf("%d服", i+1)
		}
		c.Servers = append(c.Servers, ServerConfig{ID: p, Name: ask(fmt.Sprintf("第 %d 服名称", i+1), name), Token: t})
	}
	c.AllowedGroups = splitIDs(ask("允许的群 ID（空=所有群）", strings.Join(c.AllowedGroups, ",")))
	c.AdministratorIDs = splitIDs(ask("管理员 ID（空=群管理员及群主；官方模式填写 OpenID）", strings.Join(c.AdministratorIDs, ",")))
	c.AlertGroups = splitIDs(ask("求助推送群 ID（空=不发送群通知）", strings.Join(c.AlertGroups, ",")))
	c.AlertUsers = splitIDs(ask("求助推送用户 ID（空=不发送私聊通知）", strings.Join(c.AlertUsers, ",")))
	if e := validate(c); e != nil {
		return c, e
	}
	if e := saveJSON(path, c); e != nil {
		return c, e
	}
	for _, s := range c.Servers {
		body := fmt.Sprintf("# 放入对应框架生成的插件配置；远程部署修改 bot_url\nbot_url: 'ws://127.0.0.1:17890/scpsl'\nserver_id: '%s'\nserver_name: '%s'\ntoken: '%s'\nenable_help: true\n", s.ID, strings.ReplaceAll(s.Name, "'", "''"), s.Token)
		// The listener may be changed; derive the loopback destination from its port.
		if u := strings.LastIndex(c.BridgeListen, ":"); u >= 0 {
			body = strings.Replace(body, "127.0.0.1:17890", "127.0.0.1"+c.BridgeListen[u:], 1)
		}
		if e := os.WriteFile(filepath.Join(filepath.Dir(path), "plugin-"+s.ID+".yml"), []byte(body), 0600); e != nil {
			return c, e
		}
	}
	fmt.Println("配置已保存，已生成各服 plugin-端口.yml 示例；下次双击直接启动。")
	return c, nil
}
