package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var db *sql.DB

// dataDir 数据根目录：evo.json 配置，缺省为 exe 所在目录（go run 开发时回落到工作目录）
var dataDir string

type Skill struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

// loadDataDir 依次查找 evo.json：exe 目录 → 当前工作目录
func loadDataDir() {
	dataDir = "."
	find := func(dir string) string {
		p := filepath.Join(dir, "evo.json")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
		return ""
	}
	cfg := ""
	if exe, err := os.Executable(); err == nil {
		cfg = find(filepath.Dir(exe))
	}
	if cfg == "" {
		if cwd, err := os.Getwd(); err == nil {
			cfg = find(cwd)
		}
	}
	if cfg != "" {
		if b, err := os.ReadFile(cfg); err == nil {
			var c struct {
				DataDir string `json:"data_dir"`
			}
			if json.Unmarshal(b, &c) == nil && c.DataDir != "" {
				dataDir = c.DataDir
			}
		}
	}
	// 数据根允许是新盘/同步盘路径，确保存在
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		panic(err)
	}
}

func initDB() {
	loadDataDir()
	var err error
	dbPath := filepath.ToSlash(filepath.Join(dataDir, "data.db"))
	// foreign_keys 对齐 Python 的 PRAGMA foreign_keys=ON；busy_timeout 对齐 sqlite3 默认 5s
	db, err = sql.Open("sqlite", "file:"+dbPath+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		panic(err)
	}
	// sql.Open 懒连接，真正建连要 Ping 一次，否则错误会推迟到首个请求
	if err = db.Ping(); err != nil {
		panic(err)
	}
	if err = initSchema(); err != nil {
		panic(err)
	}
	if err = maybeSeed(); err != nil {
		panic(err)
	}
}

func skillsHandler(w http.ResponseWriter, r *http.Request) {
	rows, err := db.Query("SELECT id, name, color, description FROM skills ORDER BY id")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	skills := []Skill{}
	for rows.Next() {
		var s Skill
		if err := rows.Scan(&s.ID, &s.Name, &s.Color, &s.Description); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		skills = append(skills, s)
	}
	if err := rows.Err(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(skills)
}
