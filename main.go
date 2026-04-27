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
	"strings"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

//go:embed index.html
var embeddedFiles embed.FS

type config struct {
	addr     string
	rootDir  string
	username string
	password string
}

func main() {
	cfg := &config{}

	addr := flag.String("addr", ":8080", "监听地址, 例如\":8080\"")
	dir := flag.String("d", "./", "根目录地址")
	username := flag.String("u", "", "可选用户名")
	password := flag.String("p", "", "可选密码")
	flag.Parse()

	cfg.addr = *addr
	cfg.rootDir = *dir
	cfg.username = *username
	cfg.password = *password

	fileServer := http.FileServer(http.Dir(cfg.rootDir))

	var handler http.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/upload":
			handleUpload(cfg.rootDir)(w, r)
		case r.Method == http.MethodGet && r.URL.Path == "/api/files":
			handleFileList(cfg.rootDir)(w, r)
		case r.URL.Path == "/":
			serveIndex(w, r)
		default:
			fileServer.ServeHTTP(w, r)
		}
	})

	if cfg.username != "" && cfg.password != "" {
		handler = basicAuth(handler, cfg.username, cfg.password)
	}
	handler = logRequest(handler)

	log.Printf("listen address %s", cfg.addr)
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

func handleUpload(rootDir string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 2<<30)

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

		absRoot, _ := filepath.Abs(rootDir)
		subPath := filepath.Clean(r.URL.Query().Get("path"))
		if subPath == "." || subPath == "/" {
			subPath = ""
		}
		dstDir := filepath.Join(rootDir, subPath)
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
		next.ServeHTTP(w, r)
		duration := time.Since(now)
		log.Printf("remoteAddr：%s，duration： %s，url： %s，userAgent: %s\n", r.RemoteAddr, duration, r.URL, r.Header.Get("User-Agent"))
	})
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
