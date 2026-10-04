package main

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestStandaloneExecutableFirstSetupAndQQCommands(t *testing.T) {
	binary, dll := os.Getenv("QQBOTLITE_BINARY"), os.Getenv("QQBOTLITE_PLUGIN_HARNESS")
	if binary == "" || dll == "" {
		t.Skip("set QQBOTLITE_BINARY and QQBOTLITE_PLUGIN_HARNESS for executable integration")
	}
	connected := make(chan *websocket.Conn, 1)
	responses := make(chan string, 32)
	var writes sync.Mutex
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, e := (&websocket.Upgrader{}).Upgrade(w, r, nil)
		if e != nil {
			return
		}
		defer ws.Close()
		connected <- ws
		for {
			var p struct {
				Action string `json:"action"`
				Echo   string `json:"echo"`
				Params struct {
					Message []struct {
						Type string `json:"type"`
						Data struct {
							Text string `json:"text"`
						} `json:"data"`
					} `json:"message"`
				} `json:"params"`
			}
			if ws.ReadJSON(&p) != nil {
				return
			}
			writes.Lock()
			e = ws.WriteJSON(map[string]any{"echo": p.Echo, "retcode": 0, "status": "ok", "data": map[string]int{"message_id": 99}})
			writes.Unlock()
			if e != nil {
				return
			}
			if p.Action == "send_group_msg" {
				var text strings.Builder
				for _, s := range p.Params.Message {
					text.WriteString(s.Data.Text)
				}
				responses <- text.String()
			}
		}
	}))
	defer srv.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	bridgeAddress := listener.Addr().String()
	listener.Close()
	path := filepath.Join(t.TempDir(), "config.json")
	napURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	input := strings.Join([]string{"1", napURL, "", bridgeAddress, "31140", "测试服", "456", "", "", "", ""}, "\n")
	ctx, kill := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "--config", path)
	cmd.Stdin = strings.NewReader(input)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		kill()
		t.Fatal(e)
	}
	waited := false
	defer func() {
		kill()
		if !waited {
			cmd.Wait()
		}
		if t.Failed() {
			t.Log(stdout.String(), stderr.String())
		}
	}()
	var ws *websocket.Conn
	select {
	case ws = <-connected:
	case <-time.After(5 * time.Second):
		t.Fatal("executable setup did not connect")
	}
	c, e := loadConfig(path)
	if e != nil || len(c.Servers) != 1 || c.Servers[0].Name != "测试服" || len(c.Servers[0].Token) != 64 {
		t.Fatal(c, e)
	}
	snippet, e := os.ReadFile(filepath.Join(filepath.Dir(path), "plugin-31140.yml"))
	if e != nil || !strings.Contains(string(snippet), c.Servers[0].Token) || !strings.Contains(string(snippet), strings.Split(bridgeAddress, ":")[1]) {
		t.Fatal("wizard plugin snippet")
	}
	check := exec.Command(binary, "--config", path, "--check")
	if output, e := check.CombinedOutput(); e != nil {
		t.Fatal(e, string(output))
	}
	pluginCtx, stopPlugin := context.WithCancel(context.Background())
	plugin := exec.CommandContext(pluginCtx, "dotnet", dll, "ws://"+bridgeAddress+"/scpsl", c.Servers[0].Token, "31140")
	if e = plugin.Start(); e != nil {
		stopPlugin()
		t.Fatal(e)
	}
	defer func() { stopPlugin(); plugin.Wait() }()
	seq := 0
	send := func(text, role, id string) {
		t.Helper()
		seq++
		if id == "" {
			id = strconv.Itoa(seq)
		}
		writes.Lock()
		defer writes.Unlock()
		if e := ws.WriteJSON(map[string]any{"post_type": "message", "message_type": "group", "message_id": id, "group_id": 456, "user_id": 789, "self_id": 42, "sender": map[string]string{"role": role}, "message": []any{map[string]any{"type": "text", "data": map[string]string{"text": text}}}}); e != nil {
			t.Fatal(e)
		}
	}
	reply := func() string {
		t.Helper()
		select {
		case text := <-responses:
			return text
		case <-time.After(3 * time.Second):
			t.Fatal("QQ reply missing")
			return ""
		}
	}
	end := time.Now().Add(4 * time.Second)
	for {
		send("/cx", "member", "")
		text := reply()
		if strings.Contains(text, "在线人数：3/25") {
			break
		}
		if time.Now().After(end) {
			t.Fatal("plugin never ready", text)
		}
		time.Sleep(50 * time.Millisecond)
	}
	send("/info", "member", "")
	if text := reply(); !strings.Contains(text, "下一波刷新：20秒") || !strings.Contains(text, "回合时间：2m3s") {
		t.Fatal(text)
	}
	send("/列表", "member", "")
	if text := reply(); !strings.Contains(text, "测试玩家-7") {
		t.Fatal(text)
	}
	send("round kick+7", "member", "")
	if text := reply(); !strings.Contains(text, "无管理权限") {
		t.Fatal(text)
	}
	send("round kick+7", "owner", "unique-kick")
	if text := reply(); !strings.Contains(text, "kick:7:1") {
		t.Fatal(text)
	}
	send("round kick+7", "owner", "unique-kick")
	send("helloworld", "member", "")
	if text := reply(); text != "hello world！" {
		t.Fatal("duplicate command was executed", text)
	}
	send("/绑定 123", "member", "")
	select {
	case text := <-responses:
		t.Fatal("removed command replied", text)
	case <-time.After(150 * time.Millisecond):
	}
	if stat, e := os.Stat(binary); e == nil {
		t.Logf("standalone exe: %.2f MiB", float64(stat.Size())/(1024*1024))
	}
	if runtime.GOOS == "windows" {
		output, e := exec.Command("powershell", "-NoProfile", "-Command", fmt.Sprintf("(Get-Process -Id %d).WorkingSet64", cmd.Process.Pid)).Output()
		if e == nil {
			if n, e := strconv.ParseInt(strings.TrimSpace(string(output)), 10, 64); e == nil {
				t.Logf("working set with local QQ + plugin connected: %.2f MiB", float64(n)/(1024*1024))
			}
		}
	}
	// Startup of an already configured installation must not need stdin or overwrite tokens.
	restartCtx, stopRestart := context.WithCancel(context.Background())
	restart := exec.CommandContext(restartCtx, binary, "--config", path, "--headless")
	kill()
	cmd.Wait()
	waited = true
	if e = restart.Start(); e != nil {
		stopRestart()
		t.Fatal(e)
	}
	defer func() { stopRestart(); restart.Wait() }()
	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("configured restart needs input")
	}
	after, e := loadConfig(path)
	if e != nil || after.Servers[0].Token != c.Servers[0].Token {
		t.Fatal("restart changed configuration")
	}
}
