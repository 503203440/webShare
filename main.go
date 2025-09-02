package main

import (
	"encoding/base64"
	"flag"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

type config struct {
	addr     string
	rootDir  string
	username string
	password string
}

func main() {
	cfg := &config{}

	addr := flag.String("addr", ":8080", "监听地址, 例如“:8080”")
	dir := flag.String("d", "./", "根目录地址")
	username := flag.String("u", "", "可选用户名")
	password := flag.String("p", "", "可选密码")
	flag.Parse()

	cfg.addr = *addr
	cfg.rootDir = *dir
	cfg.username = *username
	cfg.password = *password

	fs := http.FileServer(http.Dir(cfg.rootDir))

	// 如果设置了用户名和密码，才开启 Basic Auth
	if cfg.username != "" && cfg.password != "" {
		fs = basicAuth(fs, cfg.username, cfg.password)
	}

	// 日志记录
	fs = logRequest(fs)

	http.Handle("/", fs)

	log.Printf("listen address %s", cfg.addr)
	log.Fatal(http.ListenAndServe(cfg.addr, nil))

}

// ---------- Basic Auth ----------
func basicAuth(next http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const basicPrefix = "Basic "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, basicPrefix) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		// 解码 "Basic base64(user:pass)"
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, basicPrefix))
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || parts[0] != user || parts[1] != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// 日志记录
func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		next.ServeHTTP(w, r)
		duration := time.Since(now)
		log.Printf("remoteAddr：%s，duration： %s，url： %s，userAgent: %s\n", r.RemoteAddr, duration, r.URL, r.Header.Get("User-Agent"))
	})
}

// 初始化日志配置
func init() {

	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	logDir := filepath.Join(execDir, "log")
	DirInfo, err := os.Stat(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			os.MkdirAll(logDir, 0755)
		} else {
			log.Fatalf("创建日志目录失败: %v", err)
		}
	} else if !DirInfo.IsDir() {
		os.MkdirAll(logDir, 0755)
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.SetOutput(io.MultiWriter(os.Stdout, &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "webShare.log"),
		MaxSize:    10,
		MaxAge:     30,
		MaxBackups: 10,
	}))
}
