# -*- coding: utf-8 -*-
"""种子数据读取：数据存于 seed.json（Python/Go 共用的唯一数据源）。"""
import json
import os
from datetime import datetime

SEED_PATH = os.path.join(os.path.dirname(os.path.abspath(__file__)), "seed.json")


def load_seed():
    with open(SEED_PATH, encoding="utf-8") as f:
        return json.load(f)


def seed(conn):
    data = load_seed()
    leaf_ids = {}  # (skill, topic, leaf) -> node_id
    for sk in data["skills"]:
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
                cur = conn.execute(
                    "INSERT INTO nodes(skill_id, parent_id, title, evidence,"
                    " order_index, content) VALUES(?,?,?,?,?,?)",
                    (sid, tid, leaf[0], leaf[1], ni,
                     leaf[2] if len(leaf) > 2 else ""),
                )
                leaf_ids[(sk["name"], topic["title"], leaf[0])] = cur.lastrowid
        for m in data["milestones"].get(sk["name"], []):
            conn.execute(
                "INSERT INTO milestones(skill_id, title) VALUES(?,?)", (sid, m)
            )
    for i, q in enumerate(data.get("questions", [])):
        lid = leaf_ids[(q["skill"], q["topic"], q["leaf"])]
        conn.execute(
            """INSERT INTO questions(node_id, type, stem, options, answer,
               explanation, order_index) VALUES(?,?,?,?,?,?,?)""",
            (lid, q["type"], q["stem"],
             json.dumps(q["options"], ensure_ascii=False, separators=(",", ":")),
             q["answer"], q["explanation"], i),
        )
    conn.commit()
