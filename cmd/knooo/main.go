package main

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	knooo "github.com/xiejun23/Knooo"
	"github.com/xiejun23/Knooo/internal/sessions"
	"github.com/xiejun23/Knooo/internal/studio"
	"github.com/xiejun23/Knooo/internal/update"
)

//go:embed config.json
var startupConfig []byte

var assets = knooo.Assets

func main() {
	var config struct {
		Host string `json:"host"`
		Port uint16 `json:"port"`
	}
	if err := json.Unmarshal(startupConfig, &config); err != nil {
		log.Fatalf("读取启动配置 config.json：%v", err)
	}
	if config.Port == 0 {
		log.Fatal("config.json 的 port 必须在 1 到 65535 之间")
	}
	defaultAddress := net.JoinHostPort(config.Host, strconv.Itoa(int(config.Port)))
	listen := flag.String("listen", "", "Override the configured loopback address ("+defaultAddress+")")
	noOpen := flag.Bool("no-open", false, "Do not open the interface automatically")
	demo := flag.Bool("demo", false, "Use isolated example data")
	data := flag.String("data", "", "Application data directory")
	tray := flag.Bool("tray", false, "Start in the Windows notification area")
	flag.Parse()
	if *noOpen {
		attachHeadlessConsole()
	}
	if err := run(defaultAddress, *listen, *data, *noOpen, *demo, *tray); err != nil {
		if !*noOpen {
			showDesktopError(err)
		}
		log.Fatal(err)
	}
}

func run(defaultAddress, listen, data string, noOpen, demo, tray bool) error {
	canInstall := !demo && !noOpen && listen == "" && data == ""
	var desktop *desktopHost
	if !noOpen {
		var err error
		desktop, err = newDesktopHost(canInstall, tray)
		if err != nil || desktop == nil {
			return err
		}
		defer desktop.close()
	}
	listener, err := listenLocal(defaultAddress, listen)
	if err != nil {
		return err
	}
	defer listener.Close()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	if data == "" && !demo {
		dir, err := os.UserConfigDir()
		if err != nil {
			return err
		}
		data = filepath.Join(dir, "Knooo")
	}
	a, err := studio.New(sessions.New(home), data, demo)
	if err != nil {
		return err
	}
	defer a.Close()
	ui, err := fs.Sub(knooo.Assets, "ui/dist")
	if err != nil {
		return err
	}
	addr := listener.Addr().String()
	token := rand.Text()
	url := "http://" + addr + "/#open=" + token
	changelog, _ := knooo.Assets.ReadFile("CHANGELOG.md")
	stop := make(chan struct{})
	shutdown := sync.OnceFunc(func() { close(stop) })
	version := strings.TrimSpace(knooo.Version)
	updates := update.Handler(version, !demo, canInstall, shutdown)
	server := &http.Server{
		Handler:           a.Handler(addr, token, http.FileServer(http.FS(ui)), version, string(changelog), updates, desktop.topmost),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Println(url)
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	var desktopDone chan error
	if desktop != nil {
		desktopDone = make(chan error, 1)
		go func() { desktopDone <- desktop.run(url, data, stop) }()
	}

	select {
	case err = <-served:
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
	case err = <-desktopDone:
		desktopDone = nil
	case <-stop:
	}
	shutdown()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		log.Printf("关闭 Knooo 服务：%v", err)
		_ = server.Close()
	}
	if desktopDone != nil {
		if desktopErr := <-desktopDone; err == nil {
			err = desktopErr
		}
	}
	return err
}

func listenLocal(defaultAddress, override string) (net.Listener, error) {
	address := defaultAddress
	if override != "" {
		address = override
	}
	host, _, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return nil, fmt.Errorf("监听地址必须使用 127.0.0.1:<端口>，收到 %q", address)
	}
	listener, err := net.Listen("tcp4", address)
	if err == nil || override != "" {
		return listener, err
	}
	log.Printf("无法监听默认地址 %s，改用系统分配的本机端口：%v", address, err)
	return net.Listen("tcp4", net.JoinHostPort(host, "0"))
}
