package main

import (
	"database/sql"
	"encoding/json"
	"net/http"

	_ "modernc.org/sqlite"
)

var db *sql.DB

type Skill struct {
	ID          int    `json:"id"`
	Name        string `json:"name"`
	Color       string `json:"color"`
	Description string `json:"description"`
}

func initDB() {
	var err error
	// foreign_keys 对齐 Python 的 PRAGMA foreign_keys=ON；busy_timeout 对齐 sqlite3 默认 5s
	db, err = sql.Open("sqlite", "file:data.db?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
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
