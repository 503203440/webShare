package main

import (
	"encoding/base64"
	"flag"
	"log"
	"net/http"
	"strings"
)

type config struct {
	addr     string
	root     string
	username string
	password string
}

func main() {
	cfg := &config{}

	addr := flag.String("addr", ":8080", "监听地址, 例如“:8080”")
	root := flag.String("root", "./", "根目录地址")
	username := flag.String("username", "", "可选用户名")
	password := flag.String("password", "", "可选密码")
	flag.Parse()

	cfg.addr = *addr
	cfg.root = *root
	cfg.username = *username
	cfg.password = *password

	fs := http.FileServer(http.Dir(cfg.root))

	// 如果设置了用户名和密码，才开启 Basic Auth
	if cfg.username != "" && cfg.password != "" {
		fs = basicAuth(fs, cfg.username, cfg.password)
	}

	http.Handle("/", fs)

	log.Printf("listen address %s", cfg.addr)
	// log.Println("listen address :8080")
	log.Fatal(http.ListenAndServe(cfg.addr, nil))

}

// ---------- Basic Auth ----------
func basicAuth(next http.Handler, user, pass string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const basicPrefix = "Basic "
		auth := r.Header.Get("Authorization")
		if !strings.HasPrefix(auth, basicPrefix) {
			unauthorized(w)
			return
		}
		// 解码 "Basic base64(user:pass)"
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(auth, basicPrefix))
		if err != nil {
			unauthorized(w)
			return
		}
		parts := strings.SplitN(string(decoded), ":", 2)
		if len(parts) != 2 || parts[0] != user || parts[1] != pass {
			unauthorized(w)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// unauthorized 未授权
func unauthorized(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
	http.Error(w, "Unauthorized", http.StatusUnauthorized)
}
