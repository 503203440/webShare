package main

import (
	"embed"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

//go:embed index.html
//go:embed static/mpegts.js
var embeddedFiles embed.FS

type config struct {
	addr         string
	rootDir      string
	username     string
	password     string
	cert         string
	key          string
	maxUploadGB  int64
}

// Chat room types
type ChatMessage struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Content  string `json:"content"`
	Time     string `json:"time"`
	Type     string `json:"type"`
}

const (
	chatMaxTextLen  = 500
	chatMaxMessages = 500
)

var (
	chatMessages []ChatMessage
	chatMu       sync.RWMutex
	chatMsgID    atomic.Int64
)

func handleChatSend(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Content  string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "请求格式错误"})
		return
	}
	content := strings.TrimSpace(req.Content)
	if content == "" || len(content) > chatMaxTextLen {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "消息内容无效"})
		return
	}
	if req.Username == "" {
		req.Username = "匿名"
	}

	msg := ChatMessage{
		ID:       int(chatMsgID.Add(1)),
		Username: req.Username,
		Content:  content,
		Time:     time.Now().Format("2006-01-02 15:04:05"),
		Type:     "message",
	}

	chatMu.Lock()
	chatMessages = append(chatMessages, msg)
	if len(chatMessages) > chatMaxMessages {
		chatMessages = chatMessages[len(chatMessages)-chatMaxMessages:]
	}
	chatMu.Unlock()

	writeJSON(w, http.StatusOK, msg)
}

func handleChatMessages(w http.ResponseWriter, r *http.Request) {
	after := 0
	if a := r.URL.Query().Get("after"); a != "" {
		after, _ = strconv.Atoi(a)
	}

	chatMu.RLock()
	var result []ChatMessage
	for _, m := range chatMessages {
		if m.ID > after {
			result = append(result, m)
		}
	}
	chatMu.RUnlock()

	if result == nil {
		result = []ChatMessage{}
	}

	writeJSON(w, http.StatusOK, result)
}

func main() {
	cfg := &config{}

	addr := flag.String("addr", ":8080", "监听地址, 例如\":8080\"")
	dir := flag.String("d", "./", "根目录地址")
	username := flag.String("u", "", "可选用户名")
	password := flag.String("p", "", "可选密码")
	cert := flag.String("cert", "", "可选TLS证书路径")
	key := flag.String("key", "", "可选TLS密钥路径")
	maxUpload := flag.Int64("max-upload", 1, "最大上传文件大小（GB）")
	flag.Parse()

	cfg.addr = *addr
	cfg.rootDir = *dir
	cfg.username = *username
	cfg.password = *password
	cfg.cert = *cert
	cfg.key = *key
	cfg.maxUploadGB = *maxUpload

	fileServer := http.FileServer(http.Dir(cfg.rootDir))

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload":
			handleUpload(cfg)(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/files":
			handleFileList(cfg.rootDir)(w, r)
		case r.Method == http.MethodPost && r.URL.Path == "/api/chat/send":
			handleChatSend(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/chat/messages":
			handleChatMessages(w, r)
		case r.URL.Path == "/":
			serveIndex(w, r)
		case r.URL.Path == "/api/mpegts.js":
			w.Header().Set("Content-Type", "application/javascript; charset=utf-8")
			data, err := embeddedFiles.ReadFile("static/mpegts.js")
			if err != nil {
				http.Error(w, "Not Found", http.StatusNotFound)
				return
			}
			w.Write(data)
		default:
			fileServer.ServeHTTP(w, r)
		}
	})

	if cfg.username != "" && cfg.password != "" {
		handler = basicAuth(handler, cfg.username, cfg.password)
	}
	handler = logRequest(handler)

	log.Printf("listen address %s", cfg.addr)
	if cfg.cert != "" && cfg.key != "" {
		log.Printf("TLS enabled with cert=%s key=%s", cfg.cert, cfg.key)
		log.Fatal(http.ListenAndServeTLS(cfg.addr, cfg.cert, cfg.key, handler))
	}
	log.Fatal(http.ListenAndServe(cfg.addr, handler))
}

func serveIndex(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := embeddedFiles.ReadFile("index.html")
	if err != nil {
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	w.Write(data)
}

func handleUpload(cfg *config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, cfg.maxUploadGB<<30)

		err := r.ParseMultipartForm(32 << 20)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "文件太大或请求格式错误"})
			return
		}
		defer r.MultipartForm.RemoveAll()

		file, header, err := r.FormFile("file")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "未找到上传文件"})
			return
		}
		defer file.Close()

		safeName := filepath.Base(header.Filename)
		if safeName == "." || safeName == "/" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "无效的文件名"})
			return
		}

		absRoot, _ := filepath.Abs(cfg.rootDir)
		subPath := filepath.Clean(r.URL.Query().Get("path"))
		if subPath == "." || subPath == "/" {
			subPath = ""
		}
		dstDir := filepath.Join(cfg.rootDir, subPath)
		dstPath := filepath.Join(dstDir, safeName)
		absDstPath, _ := filepath.Abs(dstPath)

		if !strings.HasPrefix(absDstPath, absRoot+string(filepath.Separator)) && absDstPath != absRoot {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "路径越权"})
			return
		}

		if err := os.MkdirAll(dstDir, 0755); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法创建目录"})
			return
		}

		dst, err := os.Create(dstPath)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法创建文件"})
			return
		}
		defer dst.Close()

		_, err = io.Copy(dst, file)
		if err != nil {
			os.Remove(dstPath)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "保存文件失败"})
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"message": "ok"})
	}
}

func handleFileList(rootDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		absRoot, _ := filepath.Abs(rootDir)
		subPath := filepath.Clean(r.URL.Query().Get("path"))
		if subPath == "." || subPath == "/" {
			subPath = ""
		}
		targetDir := filepath.Join(rootDir, subPath)
		absTargetDir, _ := filepath.Abs(targetDir)

		if !strings.HasPrefix(absTargetDir, absRoot+string(filepath.Separator)) && absTargetDir != absRoot {
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "路径越权"})
			return
		}

		entries, err := os.ReadDir(targetDir)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "读取目录失败"})
			return
		}

		type fileInfo struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			ModTime string `json:"modTime"`
			IsDir   bool   `json:"isDir"`
		}

		files := make([]fileInfo, 0, len(entries))
		for _, entry := range entries {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			files = append(files, fileInfo{
				Name:    entry.Name(),
				Size:    info.Size(),
				ModTime: info.ModTime().Format("2006-01-02 15:04:05"),
				IsDir:   entry.IsDir(),
			})
		}

		writeJSON(w, http.StatusOK, files)
	}
}

func writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(data)
}

func basicAuth(next http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const basicPrefix = "Basic "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, basicPrefix) {
			w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
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

func logRequest(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		now := time.Now()
		w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate, max-age=0")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")
		mw := &MyResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		next.ServeHTTP(mw, r)
		duration := time.Since(now)
		log.Printf("url： %s，remoteAddr：%s，duration： %s，statusCode: %d，userAgent: %s\n", r.URL, GetIP(r), duration, mw.statusCode, r.Header.Get("User-Agent"))
	})
}

type MyResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (w *MyResponseWriter) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

func GetIP(r *http.Request) string {
	ip := r.Header.Get("X-Real-IP")
	if ip == "" {
		if forwarded := r.Header.Get("X-Forwarded-For"); forwarded != "" {
			if idx := strings.Index(forwarded, ","); idx != -1 {
				ip = strings.TrimSpace(forwarded[:idx])
			} else {
				ip = forwarded
			}
		}
	}
	if ip == "" {
		ip = r.RemoteAddr
	}
	return ip
}

func init() {
	execPath, _ := os.Executable()
	execDir := filepath.Dir(execPath)
	logDir := filepath.Join(execDir, "log")
	DirInfo, err := os.Stat(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			err = os.MkdirAll(logDir, 0755)
			if err != nil {
				fmt.Printf("创建日志目录失败: %v\n", err)
			}
		} else {
			fmt.Printf("访问日志目录失败: %v\n", err)
		}
	} else if !DirInfo.IsDir() {
		err = os.MkdirAll(logDir, 0755)
		if err != nil {
			fmt.Printf("创建日志目录失败: %v\n", err)
		}
	}

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
	log.SetOutput(io.MultiWriter(os.Stdout, &lumberjack.Logger{
		Filename:   filepath.Join(logDir, "webShare.log"),
		MaxSize:    10,
		MaxAge:     30,
		MaxBackups: 10,
	}))
}
