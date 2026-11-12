# -*- coding: utf-8 -*-
"""SQLite 数据层：建库、建表、种子数据。"""
import os
import sqlite3
from datetime import datetime, date

BASE_DIR = os.path.dirname(os.path.abspath(__file__))


def _data_dir():
    """读 evo.json 的 data_dir（与 Go 版同一配置），缺省为项目目录。"""
    import json
    cfg = os.path.join(BASE_DIR, "evo.json")
    if os.path.exists(cfg):
        try:
            with open(cfg, encoding="utf-8") as f:
                d = json.load(f).get("data_dir", "")
            if d:
                os.makedirs(d, exist_ok=True)
                return d
        except Exception:
            pass
    return BASE_DIR


DB_PATH = os.path.join(_data_dir(), "data.db")

INTERVALS = [1, 2, 4, 7, 15, 30]  # 复习间隔（天），艾宾浩斯简化版


def get_conn():
    conn = sqlite3.connect(DB_PATH)
    conn.row_factory = sqlite3.Row
    conn.execute("PRAGMA foreign_keys = ON")
    return conn


def status_of(mastery):
    if mastery <= 0:
        return "未开始"
    if mastery < 60:
        return "学习中"
    if mastery < 80:
        return "巩固中"
    if mastery < 95:
        return "已掌握"
    return "已精通"


def init_db():
    conn = get_conn()
    conn.executescript(
        """
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
            type TEXT NOT NULL DEFAULT 'study',   -- study / review / project
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
        );
        """
    )
    conn.commit()
    # 老库升级：nodes 缺 content 列则追加（CREATE IF NOT EXISTS 不会给已有表加列）
    cols = [r[1] for r in conn.execute("PRAGMA table_info(nodes)").fetchall()]
    if "content" not in cols:
        conn.execute("ALTER TABLE nodes ADD COLUMN content TEXT DEFAULT ''")
        conn.commit()
    if conn.execute("SELECT COUNT(*) c FROM skills").fetchone()["c"] == 0:
        from seed import seed as seed_rows
        seed_rows(conn)
    conn.close()


def leaf_avg_mastery(conn, skill_id):
    row = conn.execute(
        """SELECT AVG(n.mastery) v FROM nodes n
           WHERE n.skill_id=? AND NOT EXISTS(
             SELECT 1 FROM nodes c WHERE c.parent_id = n.id)""",
        (skill_id,),
    ).fetchone()
    return round(row["v"] or 0, 1)


def streak_days(conn):
    rows = conn.execute(
        "SELECT DISTINCT substr(created_at,1,10) d FROM logs ORDER BY d DESC LIMIT 40"
    ).fetchall()
    days = {r["d"] for r in rows}
    if not days:
        return 0
    d = date.today()
    if d.isoformat() not in days:
        d = date.fromordinal(d.toordinal() - 1)
        if d.isoformat() not in days:
            return 0
    n = 0
    while d.isoformat() in days:
        n += 1
        d = date.fromordinal(d.toordinal() - 1)
    return n


def today_tasks(conn, limit=3):
    """今日任务：先补到期复习，再推进低分学习中节点，最后开新节点。"""
    today = date.today().isoformat()
    tasks = []
    due = conn.execute(
        """SELECT n.*, s.name skill_name, s.color skill_color,
                  (julianday(?) - julianday(n.next_review_at)) overdue
           FROM nodes n JOIN skills s ON s.id=n.skill_id
           WHERE n.next_review_at IS NOT NULL AND n.next_review_at <= ?
             AND n.mastery > 0
           ORDER BY overdue DESC LIMIT ?""",
        (today, today, limit),
    ).fetchall()
    for r in due:
        tasks.append((dict(r), "复习到期"))
    if len(tasks) < limit:
        seen = [t[0]["id"] for t in tasks]
        # 注意：seen 为空时不能拼 NOT IN (NULL)，那会把结果集全部过滤掉
        excl = ""
        args = [today]
        if seen:
            excl = " AND n.id NOT IN (" + ",".join("?" * len(seen)) + ")"
            args += seen
        rows = conn.execute(
            f"""SELECT n.*, s.name skill_name, s.color skill_color
                FROM nodes n JOIN skills s ON s.id=n.skill_id
                WHERE n.mastery > 0 AND n.mastery < 80
                  AND (n.next_review_at IS NULL OR n.next_review_at > ?)
                  {excl}
                ORDER BY n.mastery ASC LIMIT ?""",
            args + [limit - len(tasks)],
        ).fetchall()
        for r in rows:
            tasks.append((dict(r), "继续推进"))
    if len(tasks) < limit:
        # 选总进度最低的技能，取其第一个未开始叶子
        skills = conn.execute("SELECT id, name FROM skills ORDER BY id").fetchall()
        skills = sorted(skills, key=lambda s: leaf_avg_mastery(conn, s["id"]))
        for s in skills:
            if len(tasks) >= limit:
                break
            row = conn.execute(
                """SELECT n.*, ? skill_name, '' skill_color
                   FROM nodes n
                   WHERE n.skill_id=? AND n.mastery=0
                     AND NOT EXISTS(SELECT 1 FROM nodes c WHERE c.parent_id=n.id)
                   ORDER BY n.order_index LIMIT 1""",
                (s["name"], s["id"]),
            ).fetchone()
            if row:
                tasks.append((dict(row), "新内容"))
    return tasks[:limit]
