# -*- coding: utf-8 -*-
"""SQLite 数据层：建库、建表、种子数据。"""
import os
import sqlite3
from datetime import datetime, date

DB_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "data.db")

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
            next_review_at TEXT
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
        """
    )
    conn.commit()
    if conn.execute("SELECT COUNT(*) c FROM skills").fetchone()["c"] == 0:
        seed(conn)
    conn.close()


def seed(conn):
    from seed import SKILLS, MILESTONES
    for sk in SKILLS:
        cur = conn.execute(
            "INSERT INTO skills(name, color, description) VALUES(?,?,?)",
            (sk["name"], sk["color"], sk.get("description", "")),
        )
        sid = cur.lastrowid
        for ti, topic in enumerate(sk["topics"]):
            cur = conn.execute(
                "INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index)"
                " VALUES(?,?,?,?,?)",
                (sid, None, topic["title"], topic.get("evidence", ""), ti),
            )
            tid = cur.lastrowid
            for ni, leaf in enumerate(topic.get("leaves", [])):
                conn.execute(
                    "INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index)"
                    " VALUES(?,?,?,?,?)",
                    (sid, tid, leaf[0], leaf[1], ni),
                )
        for m in MILESTONES.get(sk["name"], []):
            conn.execute(
                "INSERT INTO milestones(skill_id, title) VALUES(?,?)", (sid, m)
            )
    conn.commit()


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
        ph = ",".join("?" * len(seen)) or "NULL"
        rows = conn.execute(
            f"""SELECT n.*, s.name skill_name, s.color skill_color
                FROM nodes n JOIN skills s ON s.id=n.skill_id
                WHERE n.mastery > 0 AND n.mastery < 80
                  AND (n.next_review_at IS NULL OR n.next_review_at > ?)
                  AND n.id NOT IN ({ph})
                ORDER BY n.mastery ASC LIMIT ?""",
            [today] + seen + [limit - len(tasks)],
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
