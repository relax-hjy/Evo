package main

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// goldmark 默认不透传原始 HTML，课文里的标签会被转义（防注入）
var mdParser = goldmark.New(
	goldmark.WithExtensions(extension.GFM), // 表格/任务列表/删除线
)

const settingsContentDirs = "content_dirs"

// ---------- settings 存取 ----------

func getContentDirs() ([]string, error) {
	var v string
	err := db.QueryRow("SELECT value FROM settings WHERE key=?", settingsContentDirs).Scan(&v)
	if err == sql.ErrNoRows {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}
	dirs := []string{}
	if v != "" {
		if err := json.Unmarshal([]byte(v), &dirs); err != nil {
			return nil, err
		}
	}
	return dirs, nil
}

func setContentDirs(dirs []string) error {
	raw, err := json.Marshal(dirs)
	if err != nil {
		return err
	}
	_, err = db.Exec(`INSERT INTO settings(key, value) VALUES(?,?)
		ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
		settingsContentDirs, string(raw))
	return err
}

// ---------- 内容目录管理 API ----------

type contentDirItem struct {
	Path    string `json:"path"`
	Exists  bool   `json:"exists"`
	Lessons int    `json:"lessons"`
}

func countLessons(root string) int {
	n := 0
	filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			n++
		}
		return nil
	})
	return n
}

func contentDirsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case "GET":
		dirs, err := getContentDirs()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		out := []contentDirItem{}
		for _, p := range dirs {
			st, err := os.Stat(p)
			exists := err == nil && st.IsDir()
			item := contentDirItem{Path: p, Exists: exists}
			if exists {
				item.Lessons = countLessons(p)
			}
			out = append(out, item)
		}
		writeJSON(w, map[string]any{"dirs": out})

	case "POST":
		var body struct {
			Path *string `json:"path"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Path == nil || strings.TrimSpace(*body.Path) == "" {
			jsonError(w, 422, "path: Field required")
			return
		}
		p := strings.Trim(strings.TrimSpace(*body.Path), `\"`)
		st, err := os.Stat(p)
		if err != nil || !st.IsDir() {
			jsonError(w, 422, "目录不存在或不是文件夹："+p)
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			jsonError(w, 422, "非法路径")
			return
		}
		dirs, err := getContentDirs()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		for _, d := range dirs {
			if strings.EqualFold(filepath.Clean(d), filepath.Clean(abs)) {
				jsonError(w, 409, "该目录已添加")
				return
			}
		}
		dirs = append(dirs, abs)
		if err := setContentDirs(dirs); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, okResponse{OK: true})

	case "DELETE":
		var body struct {
			Path *string `json:"path"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Path == nil {
			jsonError(w, 422, "path: Field required")
			return
		}
		dirs, err := getContentDirs()
		if err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		kept := []string{}
		for _, d := range dirs {
			if !strings.EqualFold(filepath.Clean(d), filepath.Clean(*body.Path)) {
				kept = append(kept, d)
			}
		}
		if err := setContentDirs(kept); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, okResponse{OK: true})

	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		jsonError(w, http.StatusMethodNotAllowed, "Method Not Allowed")
	}
}

// ---------- 节点课文解析 ----------

// safeComponent 拒绝任何可能逃出内容根目录的路径片段
func safeComponent(s string) bool {
	return s != "" && s != "." && s != ".." &&
		!strings.ContainsAny(s, `/\:*?"<>|`) && filepath.Base(s) == s
}

// nodePathParts 取 节点的 (技能名, 主题名, 标题)；非叶子（无父）返回 ok=false
func nodePathParts(nodeID int64) (skill, topic, title string, ok bool, err error) {
	err = db.QueryRow(`SELECT s.name, tp.title, n.title FROM nodes n
		JOIN skills s ON s.id = n.skill_id
		JOIN nodes tp ON tp.id = n.parent_id
		WHERE n.id=?`, nodeID).Scan(&skill, &topic, &title)
	if err == sql.ErrNoRows {
		return "", "", "", false, nil
	}
	if err != nil {
		return "", "", "", false, err
	}
	return skill, topic, title, true, nil
}

// resolveNodeMarkdown 按 <root>/<技能>/<主题>/<标题>.md 依序查找，返回第一个命中的课文
func resolveNodeMarkdown(nodeID int64) (path string, raw string, found bool, err error) {
	skill, topic, title, isLeaf, err := nodePathParts(nodeID)
	if err != nil || !isLeaf {
		return "", "", false, err
	}
	if !safeComponent(skill) || !safeComponent(topic) || !safeComponent(title) {
		return "", "", false, nil // 标题含路径字符，放弃解析（不给攻击面）
	}
	roots, err := getContentDirs()
	if err != nil {
		return "", "", false, err
	}
	rel := filepath.Join(skill, topic, title+".md")
	for _, root := range roots {
		cand := filepath.Join(root, rel)
		// 双保险：解析后的真实路径必须仍在根目录内
		absRoot, e1 := filepath.Abs(root)
		absCand, e2 := filepath.Abs(cand)
		if e1 != nil || e2 != nil || !strings.HasPrefix(absCand, absRoot) {
			continue
		}
		b, e := os.ReadFile(cand)
		if e == nil {
			return cand, string(b), true, nil
		}
	}
	return "", "", false, nil
}

// hasMarkdownSet 为一个技能的树做一次目录扫描，返回 "主题/标题.md" 命中集合
func hasMarkdownSet(skillName string) map[string]bool {
	set := map[string]bool{}
	roots, err := getContentDirs()
	if err != nil {
		return set
	}
	for _, root := range roots {
		skillDir := filepath.Join(root, skillName)
		if st, err := os.Stat(skillDir); err != nil || !st.IsDir() {
			continue
		}
		filepath.WalkDir(skillDir, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
				return nil
			}
			rel, err := filepath.Rel(skillDir, path)
			if err == nil {
				set[filepath.ToSlash(rel)] = true
			}
			return nil
		})
	}
	return set
}

// ---------- 课文读取 API ----------

type markdownResponse struct {
	Found    bool   `json:"found"`
	Path     string `json:"path"`
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
}

func nodeMarkdownHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	path, raw, found, err := resolveNodeMarkdown(id)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	resp := markdownResponse{Found: found, Path: path}
	if found {
		resp.Markdown = raw
		var sb strings.Builder
		if err := mdParser.Convert([]byte(raw), &sb); err != nil {
			jsonError(w, http.StatusInternalServerError, err.Error())
			return
		}
		resp.HTML = sb.String()
	}
	writeJSON(w, resp)
}
