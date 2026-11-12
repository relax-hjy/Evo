package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 复习间隔（天），与 db.py INTERVALS 一致
var reviewIntervals = [...]int64{1, 2, 4, 7, 15, 30}

// treeNode 是树节点的 API 形状：10 个节点列 + 计算出的 status + 例题数
type treeNode struct {
	ID             int64   `json:"id"`
	SkillID        int64   `json:"skill_id"`
	ParentID       *int64  `json:"parent_id"`
	Title          string  `json:"title"`
	Evidence       string  `json:"evidence"`
	OrderIndex     int64   `json:"order_index"`
	Mastery        float64 `json:"mastery"`
	ReviewStage    int64   `json:"review_stage"`
	LastVerifiedAt *string `json:"last_verified_at"`
	NextReviewAt   *string `json:"next_review_at"`
	Content        string  `json:"content"`
	Status         string  `json:"status"`
	QuestionCount  int64   `json:"question_count"`
	HasMarkdown    bool    `json:"has_markdown"`
}

// ---------- 例题 ----------

type questionItem struct {
	ID          int64    `json:"id"`
	Type        string   `json:"type"`
	Stem        string   `json:"stem"`
	Options     []string `json:"options"`
	Answer      string   `json:"answer"`
	Explanation string   `json:"explanation"`
	OrderIndex  int64    `json:"order_index"`
	LastCorrect *int64   `json:"last_correct"` // 最近一次作答 0/1，null=未做过
}

type nodeStats struct {
	Attempted int64 `json:"attempted"`
	Correct   int64 `json:"correct"`
}

type questionsResponse struct {
	Questions []questionItem `json:"questions"`
	Stats     nodeStats      `json:"stats"`
}

type answerResult struct {
	OK          bool      `json:"ok"`
	Correct     bool      `json:"correct"`
	Answer      string    `json:"answer"`
	Explanation string    `json:"explanation"`
	Stats       nodeStats `json:"stats"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type idResponse struct {
	ID int64 `json:"id"`
}

type scoreResponse struct {
	OK         bool   `json:"ok"`
	NextReview string `json:"next_review"`
	Stage      int64  `json:"stage"`
}

type reviewResponse struct {
	OK         bool    `json:"ok"`
	Mastery    float64 `json:"mastery"`
	NextReview string  `json:"next_review"`
	Stage      int64   `json:"stage"`
}

// jsonError 对齐 FastAPI 错误形状：{"detail": "..."} + application/json
func jsonError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"detail": msg})
}

// only 限定 HTTP 方法（实测 Starlette 对 HEAD 也回 405，故不做 HEAD 放行）
func only(method string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method {
			h(w, r)
			return
		}
		w.Header().Set("Allow", method)
		jsonError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		jsonError(w, http.StatusUnprocessableEntity, "invalid request body")
		return false
	}
	return true
}

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		jsonError(w, http.StatusUnprocessableEntity, "invalid id")
		return 0, false
	}
	return id, true
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

// scanNode 扫描 n.* 十列（建表顺序），可空列转指针 → JSON null
func scanNode(s scanner) (treeNode, error) {
	var n treeNode
	var pid sql.NullInt64
	var lv, nr sql.NullString
	if err := s.Scan(&n.ID, &n.SkillID, &pid, &n.Title, &n.Evidence,
		&n.OrderIndex, &n.Mastery, &n.ReviewStage, &lv, &nr, &n.Content); err != nil {
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

const nodeCols = `id, skill_id, parent_id, title, evidence, order_index,
	mastery, review_stage, last_verified_at, next_review_at, content`

func loadNode(id int64) (treeNode, error) {
	row := db.QueryRow("SELECT "+nodeCols+" FROM nodes WHERE id=?", id)
	return scanNode(row)
}

// ---------- 技能树 ----------

func skillTreeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rows, err := db.Query("SELECT "+nodeCols+" FROM nodes WHERE skill_id=? ORDER BY order_index, id", id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []treeNode{}
	for rows.Next() {
		n, err := scanNode(rows)
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		n.Status = statusOf(n.Mastery)
		out = append(out, n)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 该技能全部节点的例题数，一次聚合查询
	counts := map[int64]int64{}
	crows, err := db.Query(`SELECT q.node_id, COUNT(*) FROM questions q
		JOIN nodes n ON n.id=q.node_id WHERE n.skill_id=? GROUP BY q.node_id`, id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for crows.Next() {
		var nid, c int64
		crows.Scan(&nid, &c)
		counts[nid] = c
	}
	crows.Close()
	for i := range out {
		out[i].QuestionCount = counts[out[i].ID]
	}

	// 课文文件命中集合：目录扫描一次 + 主题标题映射
	var skillName string
	db.QueryRow("SELECT name FROM skills WHERE id=?", id).Scan(&skillName)
	mdSet := hasMarkdownSet(skillName)
	if len(mdSet) > 0 {
		topicTitle := map[int64]string{}
		for _, n := range out {
			if n.ParentID == nil {
				topicTitle[n.ID] = n.Title
			}
		}
		for i := range out {
			if out[i].ParentID == nil {
				continue
			}
			if tt, ok := topicTitle[*out[i].ParentID]; ok {
				out[i].HasMarkdown = mdSet[tt+"/"+fileNameFor(out[i].Title)]
			}
		}
	}
	writeJSON(w, out)
}

func nodeStatsOf(nodeID int64) (nodeStats, error) {
	var s nodeStats
	err := db.QueryRow(
		"SELECT COUNT(*), COALESCE(SUM(correct),0) FROM answers WHERE node_id=?",
		nodeID).Scan(&s.Attempted, &s.Correct)
	return s, err
}

// nodeQuestionsHandler GET /api/nodes/{id}/questions
func nodeQuestionsHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	rows, err := db.Query(`SELECT id, type, stem, options, answer, explanation, order_index
		FROM questions WHERE node_id=? ORDER BY order_index, id`, id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	items := []questionItem{}
	for rows.Next() {
		var q questionItem
		var opts string
		if err := rows.Scan(&q.ID, &q.Type, &q.Stem, &opts, &q.Answer,
			&q.Explanation, &q.OrderIndex); err != nil {
			rows.Close()
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		q.Options = []string{}
		json.Unmarshal([]byte(opts), &q.Options)
		items = append(items, q)
	}
	rows.Close()

	// 每题最近一次作答结果（按 id 最大即最近）
	last := map[int64]int64{}
	lrows, err := db.Query(`SELECT question_id, correct FROM answers
		WHERE node_id=? AND id IN (SELECT MAX(id) FROM answers GROUP BY question_id)`, id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for lrows.Next() {
		var qid, c int64
		lrows.Scan(&qid, &c)
		last[qid] = c
	}
	lrows.Close()
	for i := range items {
		if c, ok := last[items[i].ID]; ok {
			v := c
			items[i].LastCorrect = &v
		}
	}

	stats, err := nodeStatsOf(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, questionsResponse{Questions: items, Stats: stats})
}

// answerQuestionHandler POST /api/questions/{id}/answer —— 自动判分并落作答记录
func answerQuestionHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		UserAnswer *string `json:"user_answer"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.UserAnswer == nil {
		jsonError(w, 422, "user_answer: Field required")
		return
	}

	var nodeID int64
	var stem, opts, answer, explanation string
	err := db.QueryRow(`SELECT node_id, stem, options, answer, explanation
		FROM questions WHERE id=?`, id).Scan(&nodeID, &stem, &opts, &answer, &explanation)
	if err == sql.ErrNoRows {
		jsonError(w, http.StatusNotFound, "题目不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// 判分：去空白 + 大小写不敏感（choice 的 A-D、judge 的对/错 均适用）
	got := strings.TrimSpace(*body.UserAnswer)
	correct := strings.EqualFold(got, strings.TrimSpace(answer))
	correctInt := int64(0)
	if correct {
		correctInt = 1
	}
	if _, err := db.Exec(
		"INSERT INTO answers(question_id, node_id, answered_at, user_answer, correct) VALUES(?,?,?,?,?)",
		id, nodeID, time.Now().Format("2006-01-02T15:04:05"), *body.UserAnswer, correctInt); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	stats, err := nodeStatsOf(nodeID)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, answerResult{OK: true, Correct: correct, Answer: answer,
		Explanation: explanation, Stats: stats})
}

func addNodeHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SkillID  *int64  `json:"skill_id"`
		ParentID *int64  `json:"parent_id"`
		Title    *string `json:"title"`
		Evidence *string `json:"evidence"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.SkillID == nil {
		jsonError(w, 422, "skill_id: Field required")
		return
	}
	if body.Title == nil {
		jsonError(w, 422, "title: Field required")
		return
	}
	// 标题将参与课文文件名映射（非法字符转全角），仅拒绝空与点路径
	if !safeComponent(*body.Title) {
		jsonError(w, 422, "title 不能为空或 . ..")
		return
	}
	evidence := ""
	if body.Evidence != nil {
		evidence = *body.Evidence
	}

	var mx int64
	if err := db.QueryRow(
		"SELECT COALESCE(MAX(order_index)+1,0) FROM nodes WHERE skill_id=?", *body.SkillID,
	).Scan(&mx); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res, err := db.Exec(
		"INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index) VALUES(?,?,?,?,?)",
		*body.SkillID, body.ParentID, *body.Title, evidence, mx)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	nid, _ := res.LastInsertId()
	writeJSON(w, idResponse{ID: nid})
}

func deleteNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := db.Exec("DELETE FROM nodes WHERE id=? OR parent_id=?", id, id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, okResponse{OK: true})
}

// ---------- 打分（唯一能提升掌握度的入口） ----------

func scoreNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Mastery  *float64 `json:"mastery"`
		Evidence string   `json:"evidence"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Mastery == nil {
		jsonError(w, 422, "mastery: Field required")
		return
	}

	n, err := loadNode(id)
	if err == sql.ErrNoRows {
		jsonError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now()
	ts := now.Format("2006-01-02")
	stage := n.ReviewStage
	var nxt time.Time
	if *body.Mastery >= n.Mastery { // 提分或持平 → 按节奏安排复习
		if n.LastVerifiedAt == nil || *n.LastVerifiedAt != ts {
			if n.Mastery > 0 {
				stage = min(stage+1, int64(len(reviewIntervals)-1))
			} else {
				stage = 0
			}
		}
		nxt = now.AddDate(0, 0, int(reviewIntervals[stage]))
	} else { // 降档则重置复习节奏
		stage = 0
		nxt = now.AddDate(0, 0, 1)
	}
	nxtStr := nxt.Format("2006-01-02")

	if _, err := db.Exec(
		`UPDATE nodes SET mastery=?, review_stage=?, last_verified_at=?, next_review_at=? WHERE id=?`,
		*body.Mastery, stage, ts, nxtStr, id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if body.Evidence != "" {
		if _, err := db.Exec(
			"INSERT INTO logs(created_at, node_id, skill_id, type, content) VALUES(?,?,?,'study',?)",
			time.Now().Format("2006-01-02T15:04:05"), id, n.SkillID,
			fmt.Sprintf("[打分 %d] %s：%s", int(*body.Mastery), n.Title, body.Evidence)); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	writeJSON(w, scoreResponse{OK: true, NextReview: nxtStr, Stage: stage})
}

// ---------- 复习确认 ----------

func reviewNodeHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Score *float64 `json:"score"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Score == nil {
		jsonError(w, 422, "score: Field required")
		return
	}

	n, err := loadNode(id)
	if err == sql.ErrNoRows {
		jsonError(w, http.StatusNotFound, "节点不存在")
		return
	}
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	now := time.Now()
	ts := now.Format("2006-01-02")
	stage := n.ReviewStage
	mastery := n.Mastery
	var nxt time.Time
	switch {
	case *body.Score >= 80: // 记牢：升一档、小幅加分、拉长间隔
		stage = min(stage+1, int64(len(reviewIntervals)-1))
		mastery = min(mastery+5, 100)
		nxt = now.AddDate(0, 0, int(reviewIntervals[stage]))
	case *body.Score >= 60: // 半生不熟：维持档位、缩短一半间隔
		nxt = now.AddDate(0, 0, int(max(reviewIntervals[stage]/2, 1)))
	default: // 遗忘：归零档位、降分、明天重来
		stage = 0
		mastery = max(mastery-15, 0)
		nxt = now.AddDate(0, 0, 1)
	}
	nxtStr := nxt.Format("2006-01-02")

	if _, err := db.Exec(
		`UPDATE nodes SET mastery=?, review_stage=?, last_verified_at=?, next_review_at=? WHERE id=?`,
		mastery, stage, ts, nxtStr, id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if _, err := db.Exec(
		"INSERT INTO logs(created_at, node_id, skill_id, type, content) VALUES(?,?,?,'review',?)",
		time.Now().Format("2006-01-02T15:04:05"), id, n.SkillID,
		fmt.Sprintf("[复习 %d分] %s → %s 再复习", int(*body.Score), n.Title, nxtStr)); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, reviewResponse{OK: true, Mastery: mastery, NextReview: nxtStr, Stage: stage})
}

// ---------- 日志 ----------

func listLogsHandler(w http.ResponseWriter, r *http.Request) {
	limit := int64(50)
	if q := r.URL.Query().Get("limit"); q != "" {
		v, err := strconv.ParseInt(q, 10, 64)
		if err != nil {
			jsonError(w, 422, "limit is not a valid integer")
			return
		}
		limit = v
	}
	rows, err := db.Query(`SELECT l.id, l.created_at, l.skill_id, l.node_id, l.type, l.content,
		s.name skill_name FROM logs l
		LEFT JOIN skills s ON s.id=l.skill_id ORDER BY l.id DESC LIMIT ?`, limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()

	out := []logEntry{}
	for rows.Next() {
		var l logEntry
		var sid, nid sql.NullInt64
		var sname sql.NullString
		if err := rows.Scan(&l.ID, &l.CreatedAt, &sid, &nid, &l.Type, &l.Content, &sname); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
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
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, out)
}

func addLogHandler(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Content *string `json:"content"`
		SkillID *int64  `json:"skill_id"`
		NodeID  *int64  `json:"node_id"`
		Type    *string `json:"type"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Content == nil {
		jsonError(w, 422, "content: Field required")
		return
	}
	typ := "study"
	if body.Type != nil {
		typ = *body.Type
	}
	res, err := db.Exec(
		"INSERT INTO logs(created_at, skill_id, node_id, type, content) VALUES(?,?,?,?,?)",
		time.Now().Format("2006-01-02T15:04:05"), body.SkillID, body.NodeID, typ, *body.Content)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	lid, _ := res.LastInsertId()
	writeJSON(w, idResponse{ID: lid})
}

// ---------- 里程碑 ----------

func toggleMilestoneHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Done *bool `json:"done"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Done == nil {
		jsonError(w, 422, "done: Field required")
		return
	}

	var done int64
	if *body.Done {
		done = 1
	}
	if _, err := db.Exec("UPDATE milestones SET done=? WHERE id=?", done, id); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if *body.Done {
		var sid int64
		var title string
		err := db.QueryRow("SELECT skill_id, title FROM milestones WHERE id=?", id).Scan(&sid, &title)
		if err == nil {
			if _, err := db.Exec(
				"INSERT INTO logs(created_at, skill_id, type, content) VALUES(?,?,?,?)",
				time.Now().Format("2006-01-02T15:04:05"), sid, "project",
				fmt.Sprintf("🏁 里程碑达成：%s", title)); err != nil {
				jsonError(w, http.StatusInternalServerError, err.Error())
				return
			}
		}
		// 里程碑不存在时静默跳过（与 Python 版一致）
	}
	writeJSON(w, okResponse{OK: true})
}
