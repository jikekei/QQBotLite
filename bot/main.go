package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"time"
)

func main() {
	log.SetFlags(log.Ldate | log.Ltime)
	exe, _ := os.Executable()
	defaultPath := filepath.Join(filepath.Dir(exe), "config.json")
	path := flag.String("config", defaultPath, "配置文件路径")
	wizard := flag.Bool("setup", false, "重新配置")
	check := flag.Bool("check", false, "检查配置并退出")
	headless := flag.Bool("headless", false, "不使用交互引导或退出等待")
	flag.Parse()
	if e := run(*path, *wizard, *check, *headless); e != nil {
		log.Printf("[错误] %v", e)
		if !*headless && !*check {
			fmt.Println("按回车关闭窗口。原配置未被自动覆盖。")
			_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		}
		os.Exit(1)
	}
}
func run(path string, wizard, check, headless bool) error {
	c, e := loadConfig(path)
	if wizard || (errors.Is(e, os.ErrNotExist) && !headless && !check) {
		c, e = setup(path)
	}
	if e != nil {
		return e
	}
	if check {
		fmt.Println("配置检查通过。未显示票据，未连接 QQ 或游戏服。")
		return nil
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	b := NewBridge(c.Servers)
	var a Adapter
	if c.Adapter == "napcat" {
		a = NewNapCat(c.NapCat.URL, c.NapCat.Token)
	} else {
		a = NewOfficial(c)
	}
	bot := NewBot(c, b, a, filepath.Dir(path))
	b.OnHelp = bot.Help
	mux := http.NewServeMux()
	mux.HandleFunc("/scpsl", b.Accept)
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 60 * time.Second}
	listener, e := net.Listen("tcp", c.BridgeListen)
	if e != nil {
		return fmt.Errorf("游戏插件监听失败：%w", e)
	}
	defer listener.Close()
	defer b.Close()
	errCh := make(chan error, 2)
	go func() {
		if e := srv.Serve(listener); e != nil && !errors.Is(e, http.ErrServerClosed) {
			errCh <- e
		}
	}()
	go func() { errCh <- a.Run(ctx, bot.Message) }()
	log.Printf("[启动] QQ 接入=%s；插件监听=%s；服务器=%d；Ctrl+C 退出", c.Adapter, c.BridgeListen, len(c.Servers))
	defer func() {
		stop, done := context.WithTimeout(context.Background(), 3*time.Second)
		defer done()
		_ = srv.Shutdown(stop)
	}()
	select {
	case <-ctx.Done():
		log.Println("[退出] 正在停止…")
		return nil
	case e := <-errCh:
		return e
	}
}
