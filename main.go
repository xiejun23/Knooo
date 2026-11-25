package main

import (
	"context"
	"crypto/rand"
	"embed"
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
	"time"

	"github.com/xiejun23/Knooo/internal/sessions"
	"github.com/xiejun23/Knooo/internal/studio"
	"github.com/xiejun23/Knooo/internal/update"
)

//go:embed dist CHANGELOG.md
var assets embed.FS

//go:embed config.json
var startupConfig []byte

var version = "0.1.0"

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
	flag.Parse()
	canInstall := !*demo && !*noOpen && *listen == "" && *data == ""
	listener, err := listenLocal(defaultAddress, *listen)
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	if *data == "" && !*demo {
		dir, err := os.UserConfigDir()
		if err != nil {
			log.Fatal(err)
		}
		*data = filepath.Join(dir, "Knooo")
	}
	a, err := studio.New(sessions.New(home), *data, *demo)
	if err != nil {
		log.Fatal(err)
	}
	defer a.Close()
	ui, err := fs.Sub(assets, "dist")
	if err != nil {
		log.Fatal(err)
	}
	addr := listener.Addr().String()
	token := rand.Text()
	url := "http://" + addr + "/#open=" + token
	changelog, _ := assets.ReadFile("CHANGELOG.md")
	stop := make(chan struct{})
	updates := update.Handler(version, !*demo, canInstall, func() { close(stop) })
	server := &http.Server{
		Handler:           a.Handler(addr, token, http.FileServer(http.FS(ui)), version, string(changelog), updates),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	fmt.Println(url)
	if !*noOpen {
		go func() {
			if err := openDesktop(url); err != nil {
				log.Print(err)
			}
		}()
	}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	select {
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	case <-stop:
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("关闭 Knooo 服务：%v", err)
			_ = server.Close()
		}
	}
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
