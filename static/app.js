/* EVO 前端逻辑 */
const $ = (s) => document.querySelector(s);
const $$ = (s) => [...document.querySelectorAll(s)];

let skills = [];
let currentSkill = null;
let pendingNode = null;   // 待打分节点
let pendingReview = null; // 待复习节点

async function api(path, opts = {}) {
  const res = await fetch(path, {
    headers: { "Content-Type": "application/json" },
    ...opts,
    body: opts.body ? JSON.stringify(opts.body) : undefined,
  });
  if (!res.ok) throw new Error(await res.text());
  return res.json();
}

/* ---------- 视图切换 ---------- */
$$(".nav-btn").forEach((b) =>
  b.addEventListener("click", () => {
    $$(".nav-btn").forEach((x) => x.classList.remove("active"));
    b.classList.add("active");
    $$(".view").forEach((v) => v.classList.add("hidden"));
    $("#view-" + b.dataset.view).classList.remove("hidden");
    if (b.dataset.view === "tree") renderTree();
    if (b.dataset.view === "review") renderReviews();
    if (b.dataset.view === "logs") renderLogs();
  })
);

/* ---------- 看板 ---------- */
async function renderDashboard() {
  const d = await api("/api/dashboard");
  skills = d.skills;
  $("#streak").textContent = `${d.streak} 天连续`;
  const badge = $("#due-badge");
  badge.textContent = d.due_reviews.length;
  badge.classList.toggle("hidden", d.due_reviews.length === 0);

  $("#skill-cards").innerHTML = d.skills
    .map(
      (s) => `
    <div class="card" data-skill="${s.id}">
      <div class="name"><span class="dot" style="background:${s.color}"></span>${s.name}</div>
      <div class="pct" style="color:${s.color}">${s.progress}%</div>
      <div class="status">${s.status} · ${s.leaf_total} 个知识点</div>
      <div class="bar"><i style="width:${s.progress}%;background:${s.color}"></i></div>
    </div>`
    )
    .join("");
  $$("#skill-cards .card").forEach((c) =>
    c.addEventListener("click", () => {
      currentSkill = +c.dataset.skill;
      $$(".nav-btn").find((b) => b.dataset.view === "tree").click();
    })
  );

  $("#today-tasks").innerHTML = d.today_tasks.length
    ? d.today_tasks
        .map(
          ({ node: n, reason }) => `
      <div class="task">
        <span class="reason ${reason}">${reason}</span>
        <span class="t-title">${n.title}</span>
        <span class="t-skill">${n.skill_name} · 当前 ${n.mastery} 分</span>
        <button class="btn score-btn" data-node='${JSON.stringify(n).replace(/'/g, "&#39;")}'>打分</button>
      </div>`
        )
        .join("")
    : `<div class="empty">今日暂无任务 —— 全部到复习期了，去「复习」页自测吧。</div>`;
  $$("#today-tasks .score-btn").forEach((b) =>
    b.addEventListener("click", () => openScore(JSON.parse(b.dataset.node)))
  );

  $("#milestones").innerHTML = d.milestones
    .map(
      (m) => `
    <div class="ms ${m.done ? "done" : ""}">
      <input type="checkbox" id="ms${m.id}" ${m.done ? "checked" : ""}>
      <label for="ms${m.id}"><b style="color:${skillColor(m.skill_name)}">[${m.skill_name}]</b> ${m.title}</label>
    </div>`
    )
    .join("");
  $$("#milestones input").forEach((cb, i) =>
    cb.addEventListener("change", async () => {
      const m = d.milestones[i];
      await api(`/api/milestones/${m.id}`, { method: "POST", body: { done: cb.checked } });
      renderDashboard();
    })
  );
}

function skillColor(name) {
  return skills.find((s) => s.name === name)?.color || "#777b85";
}

/* ---------- 技能树 ---------- */
async function renderTree() {
  if (!currentSkill) currentSkill = skills[0]?.id;
  $("#skill-tabs").innerHTML = skills
    .map(
      (s) => `<button class="tab ${s.id === currentSkill ? "active" : ""}" data-id="${s.id}">${s.name}</button>`
    )
    .join("");
  $$("#skill-tabs .tab").forEach((t) =>
    t.addEventListener("click", () => {
      currentSkill = +t.dataset.id;
      renderTree();
    })
  );

  const nodes = await api(`/api/skills/${currentSkill}/tree`);
  const topics = nodes.filter((n) => !n.parent_id);
  $("#tree-body").innerHTML = topics
    .map((t) => {
      const leaves = nodes.filter((n) => n.parent_id === t.id);
      const avg = leaves.length
        ? Math.round(leaves.reduce((a, b) => a + b.mastery, 0) / leaves.length)
        : 0;
      return `
      <div class="topic">
        <div class="topic-head">
          <span class="arrow">▼</span>
          <span class="t-name">${t.title}</span>
          <span class="t-ev">${t.evidence}</span>
          <span class="t-pct" style="color:${skillColor(skills.find(s=>s.id===currentSkill)?.name)}">${avg}%</span>
        </div>
        <div class="leaves">
          ${leaves
            .map(
              (l) => `
            <div class="leaf">
              <span class="l-title" title="${l.has_markdown ? "点击进入课文阅读页" : "点击展开/收起知识点正文"}">${l.title}</span>
              <span class="l-ev">${l.evidence}</span>
              <div class="mini-bar"><i style="width:${l.mastery}%;background:${barColor(l.mastery)}"></i></div>
              <span class="status-chip s-${l.status}">${l.status}</span>
              ${l.next_review_at ? `<span class="status-chip s-学习中">📅 ${l.next_review_at.slice(5)}</span>` : ""}
              ${l.question_count > 0 ? `<button class="btn ghost q-btn" data-node='${JSON.stringify(l).replace(/'/g, "&#39;")}'>例题 ${l.question_count}</button>` : ""}
              <button class="btn ghost score-btn" data-node='${JSON.stringify(l).replace(/'/g, "&#39;")}'>打分</button>
            </div>
            <div class="leaf-content hidden" data-id="${l.id}" data-md="${l.has_markdown ? 1 : 0}">${renderContent(l.content)}</div>`
            )
            .join("") || `<div class="empty">暂无子节点</div>`}
        </div>
      </div>`;
    })
    .join("");

  $$(".topic-head").forEach((h) =>
    h.addEventListener("click", () => h.parentElement.classList.toggle("closed"))
  );
  $$("#tree-body .score-btn").forEach((b) =>
    b.addEventListener("click", () => openScore(JSON.parse(b.dataset.node)))
  );
  $$("#tree-body .q-btn").forEach((b) =>
    b.addEventListener("click", () => openQuiz(JSON.parse(b.dataset.node)))
  );
  // 点知识点标题：有 md 课文 → 跳转课文阅读页；无 → 展开/收起摘要
  $$("#tree-body .l-title").forEach((t) =>
    t.addEventListener("click", async () => {
      const panel = t.closest(".leaf").nextElementSibling;
      if (!panel || !panel.classList.contains("leaf-content")) return;
      if (panel.dataset.md === "1") {
        const opened = await openReader({ id: +panel.dataset.id, title: t.textContent.trim() });
        if (opened) return;
      }
      panel.classList.toggle("hidden");
    })
  );
}

// 正文按行渲染：行首【标签】加粗，其余转义防注入
function renderContent(content) {
  if (!content) return `<div class="content-empty">（暂无正文——学习后可把笔记补进 seed.json）</div>`;
  return content
    .split("\n")
    .map((line) => {
      const safe = escapeHtml(line);
      return `<div>${safe.replace(/^(【[^】]+】)/, "<b>$1</b>")}</div>`;
    })
    .join("");
}

function barColor(m) {
  if (m >= 95) return "#8fd6ae";
  if (m >= 80) return "#65b98f";
  if (m >= 60) return "#d6a85f";
  if (m > 0) return "#7ea1dc";
  return "rgba(255,255,255,0.08)";
}

/* ---------- 打分弹窗 ---------- */
function openScore(node, note = "") {
  pendingNode = node;
  $("#modal-title").textContent = `打分：${node.title}`;
  $("#modal-evidence").textContent = node.evidence ? `验证方式：${node.evidence}` : "";
  $("#score-note").value = note;
  $("#modal").classList.remove("hidden");
}
$("#modal-cancel").addEventListener("click", () => $("#modal").classList.add("hidden"));
$$("#modal .score-btns button").forEach((b) =>
  b.addEventListener("click", async () => {
    if (!pendingNode) return;
    await api(`/api/nodes/${pendingNode.id}/score`, {
      method: "POST",
      body: { mastery: +b.dataset.score, evidence: $("#score-note").value.trim() },
    });
    $("#modal").classList.add("hidden");
    renderDashboard();
    if (!$("#view-tree").classList.contains("hidden")) renderTree();
  })
);

/* ---------- 复习 ---------- */
async function renderReviews() {
  const d = await api("/api/dashboard");
  const items = d.due_reviews;
  $("#review-list").innerHTML = items.length
    ? items
        .map(
          (r) => `
      <div class="rv-item">
        <div class="rv-main">
          <div class="rv-title">${r.title}</div>
          <div class="rv-meta">${r.skill_name} · 当前 ${r.mastery} 分 ·
            ${r.overdue > 0 ? `<span class="overdue">逾期 ${r.overdue} 天</span>` : "今天到期"}</div>
        </div>
        <button class="btn" data-rv='${JSON.stringify(r).replace(/'/g, "&#39;")}'>自测复习</button>
      </div>`
        )
        .join("")
    : `<div class="empty">🎉 没有到期复习，去学新内容吧！</div>`;
  $$("#review-list .btn[data-rv]").forEach((b) =>
    b.addEventListener("click", () => {
      pendingReview = JSON.parse(b.dataset.rv);
      $("#rv-title").textContent = `自测：${pendingReview.title}`;
      $("#review-modal").classList.remove("hidden");
    })
  );
}
$("#rv-cancel").addEventListener("click", () => $("#review-modal").classList.add("hidden"));
$$("#review-modal .score-btns button").forEach((b) =>
  b.addEventListener("click", async () => {
    if (!pendingReview) return;
    await api(`/api/nodes/${pendingReview.id}/review`, {
      method: "POST",
      body: { score: +b.dataset.rv },
    });
    $("#review-modal").classList.add("hidden");
    renderReviews();
    renderDashboard();
  })
);

/* ---------- 日志 ---------- */
async function renderLogs() {
  const logs = await api("/api/logs");
  const sel = $("#log-skill");
  if (!sel.options.length) {
    sel.innerHTML =
      `<option value="">（不关联技能）</option>` +
      skills.map((s) => `<option value="${s.id}">${s.name}</option>`).join("");
  }
  $("#log-list").innerHTML = logs.length
    ? logs
        .map(
          (l) => `
      <div class="log">
        <span class="l-time">${l.created_at.slice(0, 16).replace("T", " ")}</span>
        <span class="l-tag">${l.type === "review" ? "复习" : l.type === "project" ? "项目" : "学习"}</span>
        ${l.skill_name ? `<span class="l-tag">[${l.skill_name}]</span>` : ""}
        ${escapeHtml(l.content)}
      </div>`
        )
        .join("")
    : `<div class="empty">还没有日志，记录第一条吧。</div>`;
}

$("#log-submit").addEventListener("click", async () => {
  const content = $("#log-content").value.trim();
  if (!content) return;
  await api("/api/logs", {
    method: "POST",
    body: { content, skill_id: $("#log-skill").value ? +$("#log-skill").value : null },
  });
  $("#log-content").value = "";
  renderLogs();
});

/* ---------- 添加节点 ---------- */
$("#add-node-btn").addEventListener("click", async () => {
  if (!currentSkill) return;
  const title = prompt("新知识点标题：");
  if (!title) return;
  const evidence = prompt("验证方式（怎么证明掌握了）：") || "";
  await api("/api/nodes", {
    method: "POST",
    body: { skill_id: currentSkill, title, evidence },
  });
  renderTree();
});

/* ---------- 例题答题 ---------- */
let quiz = { node: null, items: [], idx: 0, results: {}, stats: null, autoScored: null };

async function openQuiz(node) {
  const data = await api(`/api/nodes/${node.id}/questions`);
  quiz = { node, items: data.questions, idx: 0, results: {}, stats: data.stats, autoScored: null };
  $("#quiz-title").textContent = `例题：${node.title}`;
  $("#quiz-score-btn").classList.add("hidden");
  $("#quiz-modal").classList.remove("hidden");
  renderQuestion();
}

function quizStatsText() {
  const s = quiz.stats;
  const rate = s.attempted ? Math.round((s.correct / s.attempted) * 100) : null;
  let text = `本知识点已作答 ${s.attempted} 题 · 当前答对 ${s.correct} 题` +
    (rate !== null ? `（正确率 ${rate}%）` : "");
  if (quiz.autoScored !== null) {
    text += ` · 掌握度已自动提升至 ${quiz.autoScored} 分`;
  }
  return text;
}

function renderQuestion() {
  const q = quiz.items[quiz.idx];
  const res = quiz.results[q.id];
  $("#quiz-stats").textContent = quizStatsText();
  $("#quiz-progress").textContent = `第 ${quiz.idx + 1} / ${quiz.items.length} 题`;

  const letter = (i) => (q.type === "judge" ? q.options[i] : "ABCD"[i]);
  let html = `<div class="quiz-stem">${quiz.idx + 1}. ${q.stem}</div>`;
  if (!res && q.last_correct !== null) {
    html += `<div class="quiz-last ${q.last_correct ? "last-ok" : "last-no"}">上次${q.last_correct ? "答对" : "答错"}</div>`;
  }
  html += `<div class="quiz-opts">`;
  q.options.forEach((opt, i) => {
    let cls = "";
    if (res) {
      if (letter(i) === q.answer) cls = "opt-correct";
      else if (letter(i) === res.chosen) cls = "opt-wrong";
    }
    html += `<button data-letter="${letter(i)}" class="${cls}" ${res ? "disabled" : ""}>
      <b>${letter(i)}.</b> ${opt}</button>`;
  });
  html += `</div>`;
  if (res) {
    html += `<div class="quiz-verdict ${res.correct ? "verdict-ok" : "verdict-no"}">
      ${res.correct ? "✓ 回答正确" : `✗ 回答错误（你的答案：${res.chosen}）`}
      </div><div class="quiz-exp">解析：${res.explanation}</div>`;
    if (quiz.idx < quiz.items.length - 1) {
      html += `<button id="quiz-next" class="btn">下一题 →</button>`;
    }
  }
  $("#quiz-body").innerHTML = html;

  if (!res) {
    $$("#quiz-body .quiz-opts button").forEach((b) =>
      b.addEventListener("click", () => submitAnswer(q, b.dataset.letter))
    );
  }
  const next = $("#quiz-next");
  if (next)
    next.addEventListener("click", () => {
      quiz.idx++;
      renderQuestion();
    });
}

async function submitAnswer(q, letter) {
  const res = await api(`/api/questions/${q.id}/answer`, {
    method: "POST",
    body: { user_answer: letter },
  });
  quiz.results[q.id] = { correct: res.correct, chosen: letter, explanation: res.explanation };
  quiz.stats = res.stats;
  if (res.auto_scored) quiz.autoScored = res.mastery;
  renderQuestion();
  if (Object.keys(quiz.results).length === quiz.items.length) {
    $("#quiz-score-btn").classList.remove("hidden");
  }
}

$("#quiz-close").addEventListener("click", () => {
  $("#quiz-modal").classList.add("hidden");
  if (quiz.autoScored !== null) {
    // 自动升档发生在弹窗内，关闭时刷新看板与技能树上的掌握度
    renderDashboard();
    if (!$("#view-tree").classList.contains("hidden")) renderTree();
    quiz.autoScored = null;
  }
});
$("#quiz-score-btn").addEventListener("click", () => {
  const right = Object.values(quiz.results).filter((r) => r.correct).length;
  $("#quiz-modal").classList.add("hidden");
  openScore(quiz.node, `例题 ${right}/${quiz.items.length} 正确`);
});

/* ---------- 内容目录设置 ---------- */
$("#settings-btn").addEventListener("click", () => {
  $("#settings-modal").classList.remove("hidden");
  loadContentDirs();
});
$("#settings-close").addEventListener("click", () =>
  $("#settings-modal").classList.add("hidden")
);

async function loadContentDirs() {
  const data = await api("/api/settings/content-dirs");
  $("#data-dir-label").textContent = `数据目录：${data.data_dir}`;
  $("#dir-list").innerHTML = data.dirs.length
    ? data.dirs
        .map(
          (d) => `
      <div class="dir-item">
        <span class="dir-dot ${d.exists ? "ok" : "bad"}"></span>
        <span class="dir-path" title="${d.path}">${d.path}</span>
        <span class="dir-count">${d.exists ? d.lessons + " 篇课文" : "目录不存在"}</span>
        <button class="btn ghost dir-del" data-path="${d.path}">移除</button>
      </div>`
        )
        .join("")
    : `<div class="empty">尚未添加内容目录——课文文件放哪，就把哪个目录加进来。</div>`;
  $$("#dir-list .dir-del").forEach((b) =>
    b.addEventListener("click", async () => {
      await api("/api/settings/content-dirs", {
        method: "DELETE",
        body: { path: b.dataset.path },
      });
      loadContentDirs();
    })
  );
}

$("#dir-add").addEventListener("click", async () => {
  const p = $("#dir-input").value.trim();
  if (!p) return;
  try {
    await api("/api/settings/content-dirs", { method: "POST", body: { path: p } });
    $("#dir-input").value = "";
    loadContentDirs();
  } catch (e) {
    alert("添加失败：" + e.message);
  }
});

$("#backup-btn").addEventListener("click", async () => {
  try {
    const r = await api("/api/backup", { method: "POST" });
    alert("备份完成：\n" + r.path);
  } catch (e) {
    alert("备份失败：" + e.message);
  }
});

function escapeHtml(s) {
  return s.replace(/[&<>"']/g, (c) =>
    ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c])
  );
}

/* ---------- 课文阅读页 ---------- */
let tocHeads = [];

async function openReader(node) {
  let md;
  try {
    md = await api(`/api/nodes/${node.id}/markdown`);
  } catch (e) {
    return false;
  }
  if (!md.found) return false;
  $("#reader-title").textContent = node.title;
  $("#reader-path").textContent = md.path || "";
  const box = $("#reader-content");
  box.innerHTML = md.html;
  decorateMd(box);
  buildToc(box);
  $("#reader").classList.remove("hidden");
  $("#reader-body").scrollTop = 0;
  updateReaderProgress();
  return true;
}

// 给 goldmark 产物补交互：标题锚点、代码块包装（语言标签/复制/高亮）、表格滚动包装
function decorateMd(box) {
  box.querySelectorAll("h1,h2,h3,h4").forEach((h, i) => (h.id = `md-h-${i}`));
  box.querySelectorAll("pre").forEach((pre) => {
    const code = pre.querySelector("code");
    const langCls = code ? [...code.classList].find((c) => c.startsWith("language-")) : null;
    const lang = langCls ? langCls.slice(9) : "";
    if (code && HL_KW[lang]) code.innerHTML = highlight(code.textContent, lang);

    const wrap = document.createElement("div");
    wrap.className = "codeblock";
    pre.parentNode.insertBefore(wrap, pre);
    wrap.appendChild(pre);
    if (lang) {
      const tag = document.createElement("span");
      tag.className = "code-lang";
      tag.textContent = lang;
      wrap.appendChild(tag);
    }
    const btn = document.createElement("button");
    btn.className = "copy-btn";
    btn.textContent = "复制";
    btn.addEventListener("click", async () => {
      try {
        await navigator.clipboard.writeText(code ? code.textContent : pre.textContent);
      } catch (e) { /* 剪贴板不可用时静默 */ }
      btn.textContent = "已复制";
      setTimeout(() => (btn.textContent = "复制"), 1500);
    });
    wrap.appendChild(btn);
  });
  box.querySelectorAll("table").forEach((t) => {
    const wrap = document.createElement("div");
    wrap.className = "table-wrap";
    t.parentNode.insertBefore(wrap, t);
    wrap.appendChild(t);
  });
}

function buildToc(box) {
  tocHeads = [...box.querySelectorAll("h1,h2,h3")];
  const toc = $("#reader-toc");
  if (tocHeads.length < 2) {
    toc.classList.add("hidden");
    return;
  }
  toc.classList.remove("hidden");
  toc.innerHTML =
    `<div class="toc-title">目 录</div>` +
    tocHeads
      .map((h, i) => `<a class="toc-l${h.tagName[1]}" data-i="${i}">${escapeHtml(h.textContent)}</a>`)
      .join("");
  toc.querySelectorAll("a").forEach((a) =>
    a.addEventListener("click", () =>
      tocHeads[+a.dataset.i].scrollIntoView({ behavior: "smooth", block: "start" })
    )
  );
}

function updateReaderProgress() {
  const el = $("#reader-body");
  const max = el.scrollHeight - el.clientHeight;
  $("#reader-bar").style.width = max > 0 ? (el.scrollTop / max) * 100 + "%" : "0";
  if (!tocHeads.length) return;
  const top = el.getBoundingClientRect().top;
  let active = 0;
  tocHeads.forEach((h, i) => {
    if (h.getBoundingClientRect().top - top <= 90) active = i;
  });
  $$("#reader-toc a").forEach((a, i) => a.classList.toggle("active", i === active));
}

/* 代码高亮：状态机切分 注释/字符串/数字/关键字，避免正则叠加重伤字符串内部 */
const HL_KW = {
  java: "abstract|assert|boolean|break|byte|case|catch|char|class|const|continue|default|do|double|else|enum|extends|final|finally|float|for|goto|if|implements|import|instanceof|int|interface|long|native|new|package|private|protected|public|return|short|static|super|switch|synchronized|this|throw|throws|transient|try|var|void|volatile|while|true|false|null|record|sealed|permits",
  go: "break|case|chan|const|continue|default|defer|else|fallthrough|for|func|go|goto|if|import|interface|map|package|range|return|select|struct|switch|type|var|true|false|nil|iota",
  js: "async|await|break|case|catch|class|const|continue|default|delete|do|else|export|extends|finally|for|function|if|import|in|instanceof|let|new|of|return|static|super|switch|this|throw|try|typeof|var|void|while|with|yield|true|false|null|undefined",
  ts: "async|await|break|case|catch|class|const|continue|default|delete|do|else|enum|export|extends|finally|for|from|function|if|implements|import|in|instanceof|interface|let|namespace|new|of|private|protected|public|readonly|return|static|super|switch|this|throw|try|type|typeof|var|void|while|yield|true|false|null|undefined",
  python: "and|as|assert|async|await|break|class|continue|def|del|elif|else|except|False|finally|for|from|global|if|import|in|is|lambda|None|nonlocal|not|or|pass|raise|return|True|try|while|with|yield",
};
HL_KW.javascript = HL_KW.js;
HL_KW.typescript = HL_KW.ts;
HL_KW.c = HL_KW.cpp = HL_KW.java;

function highlight(src, lang) {
  const kw = HL_KW[lang];
  if (!kw) return escapeHtml(src);
  const hashComment = lang === "python";
  const lineCom = hashComment ? "#[^\\n]*" : "//[^\\n]*";
  const re = new RegExp(
    `(${lineCom}|/\\*[\\s\\S]*?\\*/|"(?:\\\\.|[^"\\\\\\n])*"|'(?:\\\\.|[^'\\\\\\n])*')|(\\d+(?:\\.\\d+)?)|([A-Za-z_]\\w*)`,
    "g"
  );
  const kwSet = new Set(kw.split("|"));
  let out = "";
  let last = 0;
  for (const m of src.matchAll(re)) {
    out += escapeHtml(src.slice(last, m.index));
    if (m[1]) {
      const cls = m[1].startsWith("//") || m[1].startsWith("/*") || m[1].startsWith("#") ? "hl-com" : "hl-str";
      out += `<span class="${cls}">${escapeHtml(m[1])}</span>`;
    } else if (m[2]) {
      out += `<span class="hl-num">${escapeHtml(m[2])}</span>`;
    } else {
      const w = m[3];
      out += kwSet.has(w) ? `<span class="hl-kw">${escapeHtml(w)}</span>` : escapeHtml(w);
    }
    last = m.index + m[0].length;
  }
  out += escapeHtml(src.slice(last));
  return out;
}

$("#reader-back").addEventListener("click", () => $("#reader").classList.add("hidden"));
$("#reader-body").addEventListener("scroll", updateReaderProgress);
document.addEventListener("keydown", (e) => {
  if (e.key === "Escape" && !$("#reader").classList.contains("hidden"))
    $("#reader").classList.add("hidden");
});

/* ---------- 启动 ---------- */
renderDashboard();
