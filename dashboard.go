package main

import (
	"database/sql"
	"encoding/json"
	"math"
	"net/http"
	"time"
)

type dashboardSkill struct {
	Skill
	Progress  float64 `json:"progress"`
	Status    string  `json:"status"`
	LeafTotal int64   `json:"leaf_total"`
}

type taskNode struct {
	ID             int64    `json:"id"`
	SkillID        int64    `json:"skill_id"`
	ParentID       *int64   `json:"parent_id"`
	Title          string   `json:"title"`
	Evidence       string   `json:"evidence"`
	OrderIndex     int64    `json:"order_index"`
	Mastery        float64  `json:"mastery"`
	ReviewStage    int64    `json:"review_stage"`
	LastVerifiedAt *string  `json:"last_verified_at"`
	NextReviewAt   *string  `json:"next_review_at"`
	SkillName      string   `json:"skill_name"`
	SkillColor     string   `json:"skill_color"`
	Content        string   `json:"content"`
	Overdue        *float64 `json:"overdue,omitempty"` // 仅"复习到期"任务带，对齐 Python 版
}

type todayTask struct {
	Node   taskNode `json:"node"`
	Reason string   `json:"reason"`
}

type dueReview struct {
	ID           int64   `json:"id"`
	Title        string  `json:"title"`
	Mastery      float64 `json:"mastery"`
	NextReviewAt string  `json:"next_review_at"`
	SkillName    string  `json:"skill_name"`
	Overdue      int64   `json:"overdue"`
}

type milestone struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Done      int64  `json:"done"`
	SkillName string `json:"skill_name"`
}

type logEntry struct {
	ID        int64   `json:"id"`
	CreatedAt string  `json:"created_at"`
	SkillID   *int64  `json:"skill_id"`
	NodeID    *int64  `json:"node_id"`
	Type      string  `json:"type"`
	Content   string  `json:"content"`
	SkillName *string `json:"skill_name"`
}

type dashboardResponse struct {
	Skills     []dashboardSkill `json:"skills"`
	TodayTasks []todayTask      `json:"today_tasks"`
	DueReviews []dueReview      `json:"due_reviews"`
	Milestones []milestone      `json:"milestones"`
	RecentLogs []logEntry       `json:"recent_logs"`
	Streak     int              `json:"streak"`
	TodayCount int64            `json:"today_count"`
}

func statusOf(mastery float64) string {
	switch {
	case mastery <= 0:
		return "未开始"
	case mastery < 60:
		return "学习中"
	case mastery < 80:
		return "巩固中"
	case mastery < 95:
		return "已掌握"
	default:
		return "已精通"
	}
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

func todayStr() string { return time.Now().Format("2006-01-02") }

// 行扫描器：rows 与单行 Row 都满足
type scanner interface{ Scan(dest ...any) error }

// scanTaskNode 扫描 n.* + skill_name + skill_color 列序（与建表顺序一致）
func scanTaskNode(s scanner) (taskNode, error) {
	var n taskNode
	var pid sql.NullInt64
	var lv, nr sql.NullString
	if err := s.Scan(&n.ID, &n.SkillID, &pid, &n.Title, &n.Evidence,
		&n.OrderIndex, &n.Mastery, &n.ReviewStage, &lv, &nr, &n.Content,
		&n.SkillName, &n.SkillColor); err != nil {
		return n, err
	}
	if pid.Valid {
		v := pid.Int64
		n.ParentID = &v
	}
	if lv.Valid {
		v := lv.String
		n.LastVerifiedAt = &v
	}
	if nr.Valid {
		v := nr.String
		n.NextReviewAt = &v
	}
	return n, nil
}

func leafAvg(skillID int) float64 {
	var v sql.NullFloat64
	err := db.QueryRow(`SELECT AVG(n.mastery) FROM nodes n
		WHERE n.skill_id=? AND NOT EXISTS(
		SELECT 1 FROM nodes c WHERE c.parent_id = n.id)`, skillID).Scan(&v)
	if err != nil || !v.Valid {
		return 0
	}
	return round1(v.Float64)
}

func leafTotal(skillID int) int64 {
	var c int64
	db.QueryRow(`SELECT COUNT(*) FROM nodes n WHERE n.skill_id=? AND NOT EXISTS(
		SELECT 1 FROM nodes x WHERE x.parent_id = n.id)`, skillID).Scan(&c)
	return c
}

// todayTasks 复刻 db.py today_tasks 三段排序：复习到期 → 继续推进 → 新内容
func todayTasks() ([]todayTask, error) {
	today := todayStr()
	const limit = 3
	tasks := []todayTask{}

	// 段1：到期复习，按逾期天数降序
	rows, err := db.Query(`SELECT n.*, s.name skill_name, s.color skill_color,
		(julianday(?) - julianday(n.next_review_at)) overdue
		FROM nodes n JOIN skills s ON s.id=n.skill_id
		WHERE n.next_review_at IS NOT NULL AND n.next_review_at <= ?
		  AND n.mastery > 0
		ORDER BY overdue DESC LIMIT ?`, today, today, limit)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var n taskNode
		var pid sql.NullInt64
		var lv, nr sql.NullString
		var overdue sql.NullFloat64
		if err := rows.Scan(&n.ID, &n.SkillID, &pid, &n.Title, &n.Evidence,
			&n.OrderIndex, &n.Mastery, &n.ReviewStage, &lv, &nr, &n.Content,
			&n.SkillName, &n.SkillColor, &overdue); err != nil {
			rows.Close()
			return nil, err
		}
		if pid.Valid {
			v := pid.Int64
			n.ParentID = &v
		}
		if lv.Valid {
			v := lv.String
			n.LastVerifiedAt = &v
		}
		if nr.Valid {
			v := nr.String
			n.NextReviewAt = &v
		}
		if overdue.Valid {
			v := overdue.Float64
			n.Overdue = &v
		}
		tasks = append(tasks, todayTask{Node: n, Reason: "复习到期"})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()

	// 段2：学习中(0,80)且未到期，按 mastery 升序
	if len(tasks) < limit {
		seen := []any{}
		for _, t := range tasks {
			seen = append(seen, t.Node.ID)
		}
		q := `SELECT n.*, s.name skill_name, s.color skill_color
			FROM nodes n JOIN skills s ON s.id=n.skill_id
			WHERE n.mastery > 0 AND n.mastery < 80
			  AND (n.next_review_at IS NULL OR n.next_review_at > ?)`
		args := []any{today}
		if len(seen) > 0 {
			// 注意：Python 版在 seen 为空时拼 NOT IN (NULL) 会把结果集全部过滤掉，
			// 是 bug；Go 版按设计意图实现（无 seen 时跳过该子句）
			ph := ""
			for i, id := range seen {
				if i > 0 {
					ph += ","
				}
				ph += "?"
				args = append(args, id)
			}
			q += ` AND n.id NOT IN (` + ph + `)`
		}
		q += ` ORDER BY n.mastery ASC LIMIT ?`
		args = append(args, limit-len(tasks))

		rows, err := db.Query(q, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			n, err := scanTaskNode(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			tasks = append(tasks, todayTask{Node: n, Reason: "继续推进"})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}

	// 段3：总进度最低技能的第一个未开始叶子
	if len(tasks) < limit {
		type sk struct {
			id   int
			name string
			avg  float64
		}
		srows, err := db.Query("SELECT id, name FROM skills ORDER BY id")
		if err != nil {
			return nil, err
		}
		sks := []sk{}
		for srows.Next() {
			var s sk
			if err := srows.Scan(&s.id, &s.name); err != nil {
				srows.Close()
				return nil, err
			}
			sks = append(sks, s)
		}
		srows.Close()
		for i := range sks { // 与 Python sorted 稳定排序一致：平均分升序，同分保持 id 序
			sks[i].avg = leafAvg(sks[i].id)
		}
		// 插入排序保持稳定
		for i := 1; i < len(sks); i++ {
			for j := i; j > 0 && sks[j].avg < sks[j-1].avg; j-- {
				sks[j], sks[j-1] = sks[j-1], sks[j]
			}
		}
		for _, s := range sks {
			if len(tasks) >= limit {
				break
			}
			var n taskNode
			var pid sql.NullInt64
			var lv, nr sql.NullString
			err := db.QueryRow(`SELECT n.*, ? skill_name, '' skill_color
				FROM nodes n
				WHERE n.skill_id=? AND n.mastery=0
				  AND NOT EXISTS(SELECT 1 FROM nodes c WHERE c.parent_id=n.id)
				ORDER BY n.order_index LIMIT 1`, s.name, s.id).
				Scan(&n.ID, &n.SkillID, &pid, &n.Title, &n.Evidence,
					&n.OrderIndex, &n.Mastery, &n.ReviewStage, &lv, &nr, &n.Content,
					&n.SkillName, &n.SkillColor)
			if err == sql.ErrNoRows {
				continue
			}
			if err != nil {
				return nil, err
			}
			if pid.Valid {
				v := pid.Int64
				n.ParentID = &v
			}
			if lv.Valid {
				v := lv.String
				n.LastVerifiedAt = &v
			}
			if nr.Valid {
				v := nr.String
				n.NextReviewAt = &v
			}
			tasks = append(tasks, todayTask{Node: n, Reason: "新内容"})
		}
	}
	return tasks, nil
}

func streakCount() int {
	rows, err := db.Query("SELECT DISTINCT substr(created_at,1,10) FROM logs ORDER BY 1 DESC LIMIT 40")
	if err != nil {
		return 0
	}
	days := map[string]bool{}
	for rows.Next() {
		var d string
		rows.Scan(&d)
		days[d] = true
	}
	rows.Close()
	if len(days) == 0 {
		return 0
	}
	d := time.Now()
	if !days[d.Format("2006-01-02")] {
		d = d.AddDate(0, 0, -1)
		if !days[d.Format("2006-01-02")] {
			return 0
		}
	}
	n := 0
	for days[d.Format("2006-01-02")] {
		n++
		d = d.AddDate(0, 0, -1)
	}
	return n
}

func dashboardHandler(w http.ResponseWriter, r *http.Request) {
	resp := dashboardResponse{
		Skills:     []dashboardSkill{},
		TodayTasks: []todayTask{},
		DueReviews: []dueReview{},
		Milestones: []milestone{},
		RecentLogs: []logEntry{},
	}
	today := todayStr()

	srows, err := db.Query("SELECT id, name, color, description FROM skills ORDER BY id")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for srows.Next() {
		var s dashboardSkill
		if err := srows.Scan(&s.ID, &s.Name, &s.Color, &s.Description); err != nil {
			srows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		s.Progress = leafAvg(s.ID)
		s.Status = statusOf(s.Progress)
		s.LeafTotal = leafTotal(s.ID)
		resp.Skills = append(resp.Skills, s)
	}
	srows.Close()

	resp.TodayTasks, err = todayTasks()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	drows, err := db.Query(`SELECT n.id, n.title, n.mastery, n.next_review_at, s.name skill_name,
		CAST(julianday(?) - julianday(n.next_review_at) AS INT) overdue
		FROM nodes n JOIN skills s ON s.id=n.skill_id
		WHERE n.next_review_at <= ? AND n.mastery > 0
		ORDER BY overdue DESC`, today, today)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for drows.Next() {
		var d dueReview
		if err := drows.Scan(&d.ID, &d.Title, &d.Mastery, &d.NextReviewAt, &d.SkillName, &d.Overdue); err != nil {
			drows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.DueReviews = append(resp.DueReviews, d)
	}
	if err := drows.Err(); err != nil {
		drows.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	drows.Close()

	mrows, err := db.Query(`SELECT m.id, m.title, m.done, s.name skill_name FROM milestones m
		JOIN skills s ON s.id=m.skill_id ORDER BY m.skill_id, m.id`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for mrows.Next() {
		var m milestone
		if err := mrows.Scan(&m.ID, &m.Title, &m.Done, &m.SkillName); err != nil {
			mrows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		resp.Milestones = append(resp.Milestones, m)
	}
	mrows.Close()

	lrows, err := db.Query(`SELECT l.id, l.created_at, l.skill_id, l.node_id, l.type, l.content,
		s.name skill_name FROM logs l
		LEFT JOIN skills s ON s.id=l.skill_id
		ORDER BY l.id DESC LIMIT 10`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	for lrows.Next() {
		var l logEntry
		var sid, nid sql.NullInt64
		var sname sql.NullString
		if err := lrows.Scan(&l.ID, &l.CreatedAt, &sid, &nid, &l.Type, &l.Content, &sname); err != nil {
			lrows.Close()
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if sid.Valid {
			v := sid.Int64
			l.SkillID = &v
		}
		if nid.Valid {
			v := nid.Int64
			l.NodeID = &v
		}
		if sname.Valid {
			v := sname.String
			l.SkillName = &v
		}
		resp.RecentLogs = append(resp.RecentLogs, l)
	}
	lrows.Close()

	resp.Streak = streakCount()
	if err := db.QueryRow("SELECT COUNT(*) FROM logs WHERE substr(created_at,1,10)=?",
		today).Scan(&resp.TodayCount); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(resp)
}
