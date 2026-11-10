# -*- coding: utf-8 -*-
"""SkillMaster 本地学习系统 — FastAPI 后端。"""
from datetime import date, datetime, timedelta
from fastapi import FastAPI, HTTPException
from fastapi.responses import FileResponse
from fastapi.staticfiles import StaticFiles
from pydantic import BaseModel
import os

import db

app = FastAPI(title="SkillMaster")
BASE = os.path.dirname(os.path.abspath(__file__))

db.init_db()


# ---------- 模型 ----------
class ScoreIn(BaseModel):
    mastery: float            # 0/30/60/80/95
    evidence: str = ""        # 证据描述


class LogIn(BaseModel):
    content: str
    skill_id: int | None = None
    node_id: int | None = None
    type: str = "study"


class NodeIn(BaseModel):
    skill_id: int
    parent_id: int | None = None
    title: str
    evidence: str = ""


class MilestoneIn(BaseModel):
    done: bool


# ---------- 看板 ----------
@app.get("/api/dashboard")
def dashboard():
    conn = db.get_conn()
    today = date.today()
    skills = []
    for s in conn.execute("SELECT * FROM skills ORDER BY id").fetchall():
        s = dict(s)
        total = conn.execute(
            "SELECT COUNT(*) c FROM nodes WHERE skill_id=? AND NOT EXISTS"
            "(SELECT 1 FROM nodes x WHERE x.parent_id=nodes.id)",
            (s["id"],),
        ).fetchone()["c"]
        s["progress"] = db.leaf_avg_mastery(conn, s["id"])
        s["status"] = db.status_of(s["progress"])
        s["leaf_total"] = total
        skills.append(s)

    tasks = [
        {"node": n, "reason": r} for n, r in db.today_tasks(conn)
    ]
    due = conn.execute(
        """SELECT n.id, n.title, n.mastery, n.next_review_at, s.name skill_name,
                  CAST(julianday(?) - julianday(n.next_review_at) AS INT) overdue
           FROM nodes n JOIN skills s ON s.id=n.skill_id
           WHERE n.next_review_at <= ? AND n.mastery > 0
           ORDER BY overdue DESC""",
        (today.isoformat(), today.isoformat()),
    ).fetchall()

    # 里程碑
    ms = conn.execute(
        """SELECT m.id, m.title, m.done, s.name skill_name FROM milestones m
           JOIN skills s ON s.id=m.skill_id ORDER BY m.skill_id, m.id"""
    ).fetchall()

    logs = conn.execute(
        """SELECT l.*, s.name skill_name FROM logs l
           LEFT JOIN skills s ON s.id=l.skill_id
           ORDER BY l.id DESC LIMIT 10"""
    ).fetchall()

    # 本周学习分钟数按日志条数近似展示 + 今日条数
    today_count = conn.execute(
        "SELECT COUNT(*) c FROM logs WHERE substr(created_at,1,10)=?",
        (today.isoformat(),),
    ).fetchone()["c"]

    conn.close()
    return {
        "skills": skills,
        "today_tasks": tasks,
        "due_reviews": [dict(r) for r in due],
        "milestones": [dict(r) for r in ms],
        "recent_logs": [dict(r) for r in logs],
        "streak": _streak(),
        "today_count": today_count,
    }


def _streak():
    conn = db.get_conn()
    n = db.streak_days(conn)
    conn.close()
    return n


# ---------- 技能树 ----------
@app.get("/api/skills/{skill_id}/tree")
def skill_tree(skill_id: int):
    conn = db.get_conn()
    rows = conn.execute(
        "SELECT * FROM nodes WHERE skill_id=? ORDER BY order_index, id", (skill_id,)
    ).fetchall()
    conn.close()
    out = []
    for r in rows:
        d = dict(r)
        d["status"] = db.status_of(d["mastery"])
        out.append(d)
    return out


@app.post("/api/nodes")
def add_node(body: NodeIn):
    conn = db.get_conn()
    mx = conn.execute(
        "SELECT COALESCE(MAX(order_index)+1,0) m FROM nodes WHERE skill_id=?",
        (body.skill_id,),
    ).fetchone()["m"]
    cur = conn.execute(
        "INSERT INTO nodes(skill_id, parent_id, title, evidence, order_index)"
        " VALUES(?,?,?,?,?)",
        (body.skill_id, body.parent_id, body.title, body.evidence, mx),
    )
    conn.commit()
    conn.close()
    return {"id": cur.lastrowid}


@app.delete("/api/nodes/{node_id}")
def delete_node(node_id: int):
    conn = db.get_conn()
    conn.execute("DELETE FROM nodes WHERE id=? OR parent_id=?", (node_id, node_id))
    conn.commit()
    conn.close()
    return {"ok": True}


# ---------- 打分（唯一能提升掌握度的入口） ----------
@app.post("/api/nodes/{node_id}/score")
def score_node(node_id: int, body: ScoreIn):
    conn = db.get_conn()
    row = conn.execute("SELECT * FROM nodes WHERE id=?", (node_id,)).fetchone()
    if not row:
        raise HTTPException(404, "节点不存在")

    today = date.today().isoformat()
    stage = row["review_stage"] or 0
    if body.mastery >= (row["mastery"] or 0):
        # 首次达到某档位或提升 → 安排复习
        if row["last_verified_at"] != today:
            stage = min(stage + 1, len(db.INTERVALS) - 1) if row["mastery"] > 0 else 0
        nxt = (date.today() + timedelta(days=db.INTERVALS[stage])).isoformat()
    else:
        stage = 0  # 降档则重置复习节奏
        nxt = (date.today() + timedelta(days=1)).isoformat()

    conn.execute(
        """UPDATE nodes SET mastery=?, review_stage=?, last_verified_at=?,
           next_review_at=? WHERE id=?""",
        (body.mastery, stage, today, nxt, node_id),
    )
    if body.evidence:
        conn.execute(
            "INSERT INTO logs(created_at, node_id, skill_id, type, content)"
            " VALUES(?,?,?,'study',?)",
            (datetime.now().isoformat(timespec="seconds"), node_id, row["skill_id"],
             f"[打分 {int(body.mastery)}] {row['title']}：{body.evidence}"),
        )
    conn.commit()
    conn.close()
    return {"ok": True, "next_review": nxt, "stage": stage}


# ---------- 复习确认（自测后回报） ----------
class ReviewIn(BaseModel):
    score: float  # 自测得分 0-100


@app.post("/api/nodes/{node_id}/review")
def review_node(node_id: int, body: ReviewIn):
    conn = db.get_conn()
    row = conn.execute("SELECT * FROM nodes WHERE id=?", (node_id,)).fetchone()
    if not row:
        raise HTTPException(404, "节点不存在")
    today = date.today()
    stage = row["review_stage"] or 0
    mastery = row["mastery"]
    if body.score >= 80:
        stage = min(stage + 1, len(db.INTERVALS) - 1)
        mastery = min(mastery + 5, 100)   # 复习巩固小幅加分
        nxt = today + timedelta(days=db.INTERVALS[stage])
    elif body.score >= 60:
        nxt = today + timedelta(days=max(db.INTERVALS[stage] // 2, 1))
    else:
        stage = 0
        mastery = max(mastery - 15, 0)    # 遗忘则降档
        nxt = today + timedelta(days=1)

    conn.execute(
        """UPDATE nodes SET mastery=?, review_stage=?, last_verified_at=?,
           next_review_at=? WHERE id=?""",
        (mastery, stage, today.isoformat(), nxt.isoformat(), node_id),
    )
    conn.execute(
        "INSERT INTO logs(created_at, node_id, skill_id, type, content)"
        " VALUES(?,?,?,'review',?)",
        (datetime.now().isoformat(timespec="seconds"), node_id, row["skill_id"],
         f"[复习 {int(body.score)}分] {row['title']} → {nxt.isoformat()} 再复习"),
    )
    conn.commit()
    conn.close()
    return {"ok": True, "mastery": mastery, "next_review": nxt.isoformat(), "stage": stage}


# ---------- 日志 ----------
@app.get("/api/logs")
def list_logs(limit: int = 50):
    conn = db.get_conn()
    rows = conn.execute(
        """SELECT l.*, s.name skill_name FROM logs l
           LEFT JOIN skills s ON s.id=l.skill_id ORDER BY l.id DESC LIMIT ?""",
        (limit,),
    ).fetchall()
    conn.close()
    return [dict(r) for r in rows]


@app.post("/api/logs")
def add_log(body: LogIn):
    conn = db.get_conn()
    cur = conn.execute(
        "INSERT INTO logs(created_at, skill_id, node_id, type, content) VALUES(?,?,?,?,?)",
        (datetime.now().isoformat(timespec="seconds"), body.skill_id, body.node_id,
         body.type, body.content),
    )
    conn.commit()
    conn.close()
    return {"id": cur.lastrowid}


# ---------- 里程碑 ----------
@app.post("/api/milestones/{ms_id}")
def toggle_milestone(ms_id: int, body: MilestoneIn):
    conn = db.get_conn()
    conn.execute("UPDATE milestones SET done=? WHERE id=?", (1 if body.done else 0, ms_id))
    if body.done:
        row = conn.execute("SELECT skill_id, title FROM milestones WHERE id=?",
                           (ms_id,)).fetchone()
        if row:
            conn.execute(
                "INSERT INTO logs(created_at, skill_id, type, content) VALUES(?,?,?,?)",
                (datetime.now().isoformat(timespec="seconds"), row["skill_id"],
                 "project", f"🏁 里程碑达成：{row['title']}"),
            )
    conn.commit()
    conn.close()
    return {"ok": True}


# ---------- 静态页面 ----------
@app.get("/")
def index():
    return FileResponse(os.path.join(BASE, "static", "index.html"))


app.mount("/static", StaticFiles(directory=os.path.join(BASE, "static")), name="static")


if __name__ == "__main__":
    import uvicorn
    uvicorn.run(app, host="127.0.0.1", port=8765)
