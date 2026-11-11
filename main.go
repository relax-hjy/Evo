package main

import (
	"embed"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed static
var staticFS embed.FS

type healthResponse struct {
	Status  string `json:"status"`
	Service string `json:"service"`
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, healthResponse{Status: "ok", Service: "evo"})
}

// indexOrAPI 单页兜底：/api/ 未知路径回 FastAPI 风格 404，其余返回 EVO 页面
func indexOrAPI(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusNotFound)
		json.NewEncoder(w).Encode(map[string]string{"detail": "Not Found"})
		return
	}
	sub, _ := fs.Sub(staticFS, "static")
	http.ServeFileFS(w, r, sub, "index.html")
}

// logsHandler 同一路径支持 GET/POST 两种方法
func logsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		listLogsHandler(w, r)
	case "POST":
		addLogHandler(w, r)
	default:
		// 与 Starlette 一致：405 的 Allow 只报该路径首个注册方法
		w.Header().Set("Allow", "GET")
		jsonError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func main() {
	initDB()

	mux := http.NewServeMux()

	mux.HandleFunc("/api/health", only("GET", healthHandler))
	mux.HandleFunc("/api/dashboard", only("GET", dashboardHandler))
	mux.HandleFunc("/api/skills", only("GET", skillsHandler))
	mux.HandleFunc("/api/skills/{id}/tree", only("GET", skillTreeHandler))
	mux.HandleFunc("/api/nodes", only("POST", addNodeHandler))
	mux.HandleFunc("/api/nodes/{id}", only("DELETE", deleteNodeHandler))
	mux.HandleFunc("/api/nodes/{id}/score", only("POST", scoreNodeHandler))
	mux.HandleFunc("/api/nodes/{id}/review", only("POST", reviewNodeHandler))
	mux.HandleFunc("/api/nodes/{id}/questions", only("GET", nodeQuestionsHandler))
	mux.HandleFunc("/api/nodes/{id}/markdown", only("GET", nodeMarkdownHandler))
	mux.HandleFunc("/api/questions/{id}/answer", only("POST", answerQuestionHandler))
	mux.HandleFunc("/api/settings/content-dirs", contentDirsHandler)
	mux.HandleFunc("/api/logs", logsHandler)
	mux.HandleFunc("/api/milestones/{id}", only("POST", toggleMilestoneHandler))

	// /static/xxx → static/xxx（内嵌文件系统，编译进 exe）
	sub, _ := fs.Sub(staticFS, "static")
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.FS(sub))))

	mux.HandleFunc("/", indexOrAPI)

	if err := http.ListenAndServe("127.0.0.1:8765", mux); err != nil {
		panic(err)
	}
}
