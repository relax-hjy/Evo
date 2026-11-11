package main

import (
	"bytes"
	"database/sql"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
)

// marshalOptions 紧凑 JSON 且不 HTML 转义（对齐 Python json.dumps 字节格式）
func marshalOptions(opts []string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(opts); err != nil {
		return "", err
	}
	return strings.TrimRight(buf.String(), "\n"), nil
}

//go:embed seed.json
var seedJSON []byte

type seedTopic struct {
	Title    string     `json:"title"`
	Evidence string     `json:"evidence"`
	Leaves   [][]string `json:"leaves"` // [title, evidence]
}

type seedSkill struct {
	Name        string      `json:"name"`
	Color       string      `json:"color"`
	Description string      `json:"description"`
	Topics      []seedTopic `json:"topics"`
}

type seedQuestion struct {
	Skill       string   `json:"skill"`
	Topic       string   `json:"topic"`
	Leaf        string   `json:"leaf"`
	Type        string   `json:"type"`
	Stem        string   `json:"stem"`
	Options     []string `json:"options"`
	Answer      string   `json:"answer"`
	Explanation string   `json:"explanation"`
}

type seedData struct {
	Skills     []seedSkill         `json:"skills"`
	Milestones map[string][]string `json:"milestones"`
	Questions  []seedQuestion      `json:"questions"`
}

// 与 db.py init_db 的 executescript 逐字一致
const schemaSQL = `
CREATE TABLE IF NOT EXISTS skills (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    color TEXT NOT NULL DEFAULT '#4f8cff',
    description TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS nodes (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    parent_id INTEGER REFERENCES nodes(id),
    title TEXT NOT NULL,
    evidence TEXT DEFAULT '',
    order_index INTEGER DEFAULT 0,
    mastery REAL DEFAULT 0,
    review_stage INTEGER DEFAULT 0,
    last_verified_at TEXT,
    next_review_at TEXT,
    content TEXT DEFAULT ''
);
CREATE TABLE IF NOT EXISTS logs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    created_at TEXT NOT NULL,
    skill_id INTEGER REFERENCES skills(id),
    node_id INTEGER REFERENCES nodes(id),
    type TEXT NOT NULL DEFAULT 'study',
    content TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS milestones (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    skill_id INTEGER NOT NULL REFERENCES skills(id),
    title TEXT NOT NULL,
    done INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS questions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    node_id INTEGER NOT NULL REFERENCES nodes(id),
    type TEXT NOT NULL DEFAULT 'choice',
    stem TEXT NOT NULL,
    options TEXT NOT NULL DEFAULT '[]',
    answer TEXT NOT NULL,
    explanation TEXT DEFAULT '',
    order_index INTEGER DEFAULT 0
);
CREATE TABLE IF NOT EXISTS answers (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    question_id INTEGER NOT NULL REFERENCES questions(id),
    node_id INTEGER NOT NULL REFERENCES nodes(id),
    answered_at TEXT NOT NULL,
    user_answer TEXT NOT NULL,
    correct INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_questions_node ON questions(node_id);
CREATE INDEX IF NOT EXISTS idx_answers_node ON answers(node_id);
CREATE TABLE IF NOT EXISTS settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL
);`

func initSchema() error {
	if _, err := db.Exec(schemaSQL); err != nil {
		return err
	}
	// 老库升级：nodes 表缺 content 列则追加（CREATE IF NOT EXISTS 不会给已有表加列）
	var cnt int
	if err := db.QueryRow(
		`SELECT COUNT(*) FROM pragma_table_info('nodes') WHERE name='content'`).Scan(&cnt); err != nil {
		return err
	}
	if cnt == 0 {
		if _, err := db.Exec(`ALTER TABLE nodes ADD COLUMN content TEXT DEFAULT ''`); err != nil {
			return err
		}
	}
	return nil
}

// maybeSeed 空库时从 seed.json 播种（与 Python seed.py 同一数据源、同一插入顺序）；
// 已有数据的老库则回填例题（建表由 initSchema 的 IF NOT EXISTS 完成）
func maybeSeed() error {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM skills").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		if err := backfillQuestions(); err != nil {
			return err
		}
		return backfillContents()
	}

	var data seedData
	if err := json.Unmarshal(seedJSON, &data); err != nil {
		return fmt.Errorf("parse seed.json: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() // Commit 后为无害 no-op

	type leafKey struct{ skill, topic, leaf string }
	leafIDs := map[leafKey]int64{}

	for _, sk := range data.Skills {
		res, err := tx.Exec("INSERT INTO skills(name, color, description) VALUES(?,?,?)",
			sk.Name, sk.Color, sk.Description)
		if err != nil {
			return fmt.Errorf("insert skill %s: %w", sk.Name, err)
		}
		sid, _ := res.LastInsertId()

		for ti, t := range sk.Topics {
			res, err := tx.Exec(
				"INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index) VALUES(?,?,?,?,?)",
				sid, nil, t.Title, t.Evidence, ti)
			if err != nil {
				return fmt.Errorf("insert topic %s: %w", t.Title, err)
			}
			tid, _ := res.LastInsertId()

			for ni, leaf := range t.Leaves {
				content := ""
				if len(leaf) > 2 {
					content = leaf[2]
				}
				res, err := tx.Exec(
					"INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index, content) VALUES(?,?,?,?,?,?)",
					sid, tid, leaf[0], leaf[1], ni, content)
				if err != nil {
					return fmt.Errorf("insert leaf %s: %w", leaf[0], err)
				}
				lid, _ := res.LastInsertId()
				leafIDs[leafKey{sk.Name, t.Title, leaf[0]}] = lid
			}
		}
		for _, m := range data.Milestones[sk.Name] {
			if _, err := tx.Exec("INSERT INTO milestones(skill_id, title) VALUES(?,?)",
				sid, m); err != nil {
				return fmt.Errorf("insert milestone %s: %w", m, err)
			}
		}
	}

	for i, q := range data.Questions {
		lid, ok := leafIDs[leafKey{q.Skill, q.Topic, q.Leaf}]
		if !ok {
			return fmt.Errorf("question %d references unknown leaf: %s/%s/%s",
				i, q.Skill, q.Topic, q.Leaf)
		}
		opts, err := marshalOptions(q.Options)
		if err != nil {
			return fmt.Errorf("question %d options: %w", i, err)
		}
		if _, err := tx.Exec(
			`INSERT INTO questions(node_id, type, stem, options, answer, explanation, order_index)
			 VALUES(?,?,?,?,?,?,?)`,
			lid, q.Type, q.Stem, opts, q.Answer, q.Explanation, i); err != nil {
			return fmt.Errorf("insert question %d: %w", i, err)
		}
	}
	return tx.Commit()
}

// backfillQuestions 老库升级：已有技能数据但没题目时，按 seed.json 路径补插例题
func backfillQuestions() error {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM questions").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	var data seedData
	if err := json.Unmarshal(seedJSON, &data); err != nil {
		return fmt.Errorf("parse seed.json: %w", err)
	}
	for i, q := range data.Questions {
		var lid int64
		err := db.QueryRow(`SELECT n.id FROM nodes n
			JOIN skills s ON s.id = n.skill_id
			JOIN nodes t ON t.id = n.parent_id
			WHERE s.name=? AND t.title=? AND n.title=?`,
			q.Skill, q.Topic, q.Leaf).Scan(&lid)
		if err == sql.ErrNoRows {
			return fmt.Errorf("question %d references unknown leaf: %s/%s/%s",
				i, q.Skill, q.Topic, q.Leaf)
		}
		if err != nil {
			return err
		}
		opts, err := marshalOptions(q.Options)
		if err != nil {
			return fmt.Errorf("question %d options: %w", i, err)
		}
		if _, err := db.Exec(
			`INSERT INTO questions(node_id, type, stem, options, answer, explanation, order_index)
			 VALUES(?,?,?,?,?,?,?)`,
			lid, q.Type, q.Stem, opts, q.Answer, q.Explanation, i); err != nil {
			return fmt.Errorf("insert question %d: %w", i, err)
		}
	}
	return nil
}

// backfillContents 老库升级：叶子节点 content 为空时按 seed.json 路径回填学习正文
func backfillContents() error {
	var n int
	if err := db.QueryRow(
		"SELECT COUNT(*) FROM nodes WHERE COALESCE(content,'') <> ''").Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}

	var data seedData
	if err := json.Unmarshal(seedJSON, &data); err != nil {
		return fmt.Errorf("parse seed.json: %w", err)
	}
	for _, sk := range data.Skills {
		for _, t := range sk.Topics {
			for _, leaf := range t.Leaves {
				if len(leaf) < 3 || leaf[2] == "" {
					continue
				}
				res, err := db.Exec(`UPDATE nodes SET content=? WHERE id=(
					SELECT n.id FROM nodes n
					JOIN skills s ON s.id = n.skill_id
					JOIN nodes tp ON tp.id = n.parent_id
					WHERE s.name=? AND tp.title=? AND n.title=?)`,
					leaf[2], sk.Name, t.Title, leaf[0])
				if err != nil {
					return fmt.Errorf("backfill content %s: %w", leaf[0], err)
				}
				if rows, _ := res.RowsAffected(); rows == 0 {
					return fmt.Errorf("backfill content: unknown leaf %s/%s/%s",
						sk.Name, t.Title, leaf[0])
				}
			}
		}
	}
	return nil
}
