// Command server 提供 ipgeo 的只读 HTTP 接口。
//
// 单文件、仅依赖标准库与 github.com/justseemore/ipgeo：
//
//	go run ./cmd/server
//	go build -o bin/ipgeo-server ./cmd/server
//
// 接口：
//
//	GET  /api/v1/ip/{ip}?format=text   单条查询（RESTful）
//	GET  /ip.json?ip=1.1.1.1          单条查询（后缀式捷径）
//	GET  /ip.txt?ip=1.1.1.1           单条查询（纯文本捷径）
//	GET  /                            查询调用方自身 IP
//	GET  /healthz  /readyz            探针
//	POST /api/v1/admin/reload         热加载（需 IPGEO_ADMIN_TOKEN）
//
// 环境变量：
//
//	IPGEO_ADDR              监听地址，默认 :8080
//	IPGEO_V4_XDB            IPv4 库路径，默认 /data/ip2region_v4.xdb；off 表示禁用
//	IPGEO_V6_XDB            IPv6 库路径，默认 /data/ip2region_v6.xdb；off 表示禁用
//	IPGEO_LRU_ENTRIES       结果缓存条数，默认 200000
//	IPGEO_ADMIN_TOKEN       为空则禁用 /admin/reload
//	IPGEO_TRUST_PROXY       true 时信任 X-Forwarded-For
//	IPGEO_WATCH_INTERVAL    本地库文件轮询间隔，默认 30s
package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/justseemore/ipgeo"
)

// version 由 -ldflags "-X main.version=..." 注入。
var version = "dev"

var lookups atomic.Uint64

func main() {
	health := flag.Bool("healthcheck", false, "探测本机 /readyz 后退出，供容器 HEALTHCHECK 使用")
	flag.Parse()

	addr := env("IPGEO_ADDR", ":8080")
	if *health {
		os.Exit(probe(addr))
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	v4 := xdbPath("IPGEO_V4_XDB", "/data/ip2region_v4.xdb")
	v6 := xdbPath("IPGEO_V6_XDB", "/data/ip2region_v6.xdb")
	lru, _ := strconv.Atoi(env("IPGEO_LRU_ENTRIES", "200000"))
	token := env("IPGEO_ADMIN_TOKEN", "")
	trustProxy := env("IPGEO_TRUST_PROXY", "false") == "true"
	watch, err := time.ParseDuration(env("IPGEO_WATCH_INTERVAL", "30s"))
	if err != nil {
		log.Warn("IPGEO_WATCH_INTERVAL 非法，回退 30s", "err", err)
		watch = 30 * time.Second
	}

	// Provider 内部持有可热切换的 DB 句柄：Lookup 走原子指针，Reload 时旧库等
	// 在途查询排空后再 Munmap。
	db, err := ipgeo.New(v4, v6, log, ipgeo.WithLRU(lru))
	if err != nil {
		log.Error("加载 xdb 失败", "v4", v4, "v6", v6, "err", err)
		os.Exit(1)
	}
	defer db.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if watch > 0 {
		go db.Watch(ctx, watch) // 本地库文件 size/mtime 变化即热加载
	}
	go db.WatchSIGHUP(ctx) // 仅 unix：kill -HUP 立即重载

	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/v1/ip/{ip}", func(w http.ResponseWriter, r *http.Request) {
		lookup(w, db, strings.TrimSpace(r.PathValue("ip")), pickFormat(r))
	})

	mux.HandleFunc("GET /ip.json", func(w http.ResponseWriter, r *http.Request) {
		lookup(w, db, strings.TrimSpace(r.URL.Query().Get("ip")), "json")
	})

	mux.HandleFunc("GET /ip.txt", func(w http.ResponseWriter, r *http.Request) {
		lookup(w, db, strings.TrimSpace(r.URL.Query().Get("ip")), "text")
	})

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})

	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		// 真查一次，确认 mmap 的数据仍可用；未收录也算就绪。
		if v4 != "" {
			if _, err := db.Lookup("223.5.5.5"); err != nil && !errors.Is(err, ipgeo.ErrNotFound) {
				writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "version": version})
	})

	mux.HandleFunc("POST /api/v1/admin/reload", func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			writeJSON(w, http.StatusForbidden, errBody{"reload_disabled", "未设置 IPGEO_ADMIN_TOKEN"})
			return
		}
		got := r.Header.Get("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, errBody{"unauthorized", "X-Admin-Token 不正确"})
			return
		}

		c, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		if err := db.Reload(c); err != nil {
			log.Error("热加载失败，继续使用旧库", "err", err)
			writeJSON(w, http.StatusInternalServerError, errBody{"reload_failed", err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"reloaded": true, "at": time.Now().UTC()})
	})

	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			writeJSON(w, http.StatusNotFound, errBody{"not_found", "未知路由"})
			return
		}
		lookup(w, db, clientIP(r, trustProxy), pickFormat(r))
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
		ErrorLog:          slog.NewLogLogger(log.Handler(), slog.LevelWarn),
	}

	errCh := make(chan error, 1)
	go func() {
		log.Info("HTTP 服务启动",
			"addr", addr, "version", version,
			"v4", v4, "v6", v6, "lru", lru, "watch", watch.String())
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		log.Error("监听失败", "err", err)
		os.Exit(1)
	case <-ctx.Done():
	}

	log.Info("收到退出信号，开始优雅关闭")
	sc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(sc); err != nil {
		log.Error("优雅关闭失败", "err", err)
	}
	log.Info("已退出", "lookups", lookups.Load())
}

// ---------------------------------------------------------------- 查询

type errBody struct {
	Error   string `json:"error"`
	Message string `json:"message,omitempty"`
}

func lookup(w http.ResponseWriter, db *ipgeo.Provider, ip, format string) {
	if ip == "" {
		writeJSON(w, http.StatusBadRequest, errBody{"missing_ip", "缺少 ip 参数，例如 /ip.json?ip=1.1.1.1"})
		return
	}
	lookups.Add(1)

	info, err := db.Lookup(ip)
	switch {
	case err == nil:
	case errors.Is(err, ipgeo.ErrInvalidIP):
		writeJSON(w, http.StatusBadRequest, errBody{"invalid_ip", ip})
		return
	case errors.Is(err, ipgeo.ErrNotFound):
		writeJSON(w, http.StatusNotFound, errBody{"not_found", ip})
		return
	case errors.Is(err, ipgeo.ErrClosed):
		writeJSON(w, http.StatusServiceUnavailable, errBody{"unavailable", "服务正在关闭"})
		return
	default:
		slog.Error("查询失败", "ip", ip, "err", err)
		writeJSON(w, http.StatusInternalServerError, errBody{"internal", ""})
		return
	}

	if format == "text" {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, info.String()+"\n")
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// ---------------------------------------------------------------- 工具

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

// xdbPath 允许用 off / - 显式禁用某个版本。
func xdbPath(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	switch {
	case v == "":
		return def
	case strings.EqualFold(v, "off"), v == "-":
		return ""
	}
	return v
}

func pickFormat(r *http.Request) string {
	switch strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format"))) {
	case "text", "txt", "plain":
		return "text"
	}
	return "json"
}

func clientIP(r *http.Request, trustProxy bool) string {
	if trustProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if first := strings.TrimSpace(strings.Split(xff, ",")[0]); first != "" {
				return first
			}
		}
		if xr := strings.TrimSpace(r.Header.Get("X-Real-IP")); xr != "" {
			return xr
		}
	}
	if i := strings.LastIndex(r.RemoteAddr, ":"); i > 0 {
		return strings.Trim(r.RemoteAddr[:i], "[]")
	}
	return r.RemoteAddr
}

// probe 让容器不依赖 curl/wget 也能做健康检查。
func probe(addr string) int {
	port := "8080"
	if i := strings.LastIndex(addr, ":"); i >= 0 && i < len(addr)-1 {
		port = addr[i+1:]
	}
	url := "http://127.0.0.1:" + port + "/readyz"

	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck %s: %v\n", url, err)
		return 1
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck %s: status %d\n", url, resp.StatusCode)
		return 1
	}
	return 0
}
