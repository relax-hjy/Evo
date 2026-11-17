// e2e-questions.js —— 例题链路端到端验证
// 默认只读：校验 Java/语言基础 8 叶子题数 ≥100
// 破坏性作答流（自动升档/统计口径断言）需显式 EVO_E2E=1，且仅允许打在全新实例上
//（要求目标叶子作答数为 0）——请对 .tmp-run 隔离实例运行，勿对真实数据根执行
// 用法：EVO_E2E=1 node tools/e2e-questions.js [baseUrl]
const BASE = process.argv[2] || "http://127.0.0.1:8765";
const DO_ANSWER = process.env.EVO_E2E === "1";
const TOPIC = "语言基础";

let failed = 0;
function check(cond, msg) {
  if (cond) console.log(`  ok  ${msg}`);
  else { failed++; console.error(`  FAIL ${msg}`); }
}
async function j(path, opts) {
  const r = await fetch(BASE + path, opts);
  const body = await r.json().catch(() => null);
  if (!r.ok) throw new Error(`${opts?.method || "GET"} ${path} → ${r.status} ${JSON.stringify(body)}`);
  return body;
}
const post = (path, body) =>
  j(path, { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });

(async () => {
  console.log(`目标：${BASE}`);
  const health = await j("/api/health");
  check(health.status === "ok", "health ok");

  const skills = await j("/api/skills");
  const java = skills.find((s) => s.name === "Java");
  const tree = await j(`/api/skills/${java.id}/tree`);
  const topicOf = Object.fromEntries(tree.filter((n) => n.parent_id === null).map((n) => [n.id, n.title]));
  const leaves = tree.filter((n) => n.parent_id !== null && topicOf[n.parent_id] === TOPIC);
  check(leaves.length === 8, `语言基础 8 个叶子（实际 ${leaves.length}）`);
  for (const l of leaves) check(l.question_count >= 100, `${l.title} 题数 ${l.question_count} ≥100`);

  if (!DO_ANSWER) {
    console.log("只读模式通过（设置 EVO_E2E=1 对隔离实例运行作答流验证）");
    process.exit(failed ? 1 : 0);
  }

  // ---- 破坏性作答流：仅限全新实例 ----
  const leaf = leaves.find((l) => l.title === "变量与基本类型");
  let data = await j(`/api/nodes/${leaf.id}/questions`);
  check(data.questions.length >= 100, `取题 ${data.questions.length} 道`);
  if (data.stats.attempted !== 0) {
    console.error(`拒绝运行：${leaf.title} 已有 ${data.stats.attempted} 条作答，非全新实例`);
    process.exit(1);
  }

  const qs = data.questions;
  const wrongOf = (q) => (q.type === "judge" ? (q.answer === "对" ? "错" : "对") : q.answer === "A" ? "B" : "A");
  const answer = (q, val) => post(`/api/questions/${q.id}/answer`, { user_answer: val });

  let res = await answer(qs[0], wrongOf(qs[0]));
  check(res.correct === false, "首题答错判错");
  check(res.stats.attempted === 1 && res.stats.correct === 0, `统计口径 {${res.stats.attempted},${res.stats.correct}}`);

  // 30 题（含首题错）→ 60 档自动升档
  for (let i = 1; i < 30; i++) res = await answer(qs[i], qs[i].answer);
  check(res.auto_scored === true && res.mastery === 60, `30 题 96.7% → 自动升 60（mastery=${res.mastery}）`);
  check(res.stats.attempted === 30 && res.stats.correct === 29, `统计 {${res.stats.attempted},${res.stats.correct}}`);

  // 50 题 → 80 档
  for (let i = 30; i < 50; i++) res = await answer(qs[i], qs[i].answer);
  check(res.auto_scored === true && res.mastery === 80, `50 题 98% → 自动升 80（mastery=${res.mastery}）`);

  // 80 题 → 95 档
  for (let i = 50; i < 80; i++) res = await answer(qs[i], qs[i].answer);
  check(res.auto_scored === true && res.mastery === 95, `80 题 98.75% → 自动升 95（mastery=${res.mastery}）`);

  // 翻转语义：首题错题重刷对 → attempted 不变、correct +1、不再触发升档
  res = await answer(qs[0], qs[0].answer);
  check(res.stats.attempted === 80 && res.stats.correct === 80,
    `重刷错题 → {${res.stats.attempted},${res.stats.correct}}（题数不变、当前全对）`);
  check(res.auto_scored === false && res.mastery === 95, "已达 95 不重复升档");

  // 证据日志与复习节奏
  const logs = await j("/api/logs?limit=10");
  check(logs.some((l) => l.content.includes("例题自动评价：已作答 80 题")), "证据日志含最新口径文案");
  const node = tree.find((n) => n.id === leaf.id);
  const after = (await j(`/api/skills/${java.id}/tree`)).find((n) => n.id === leaf.id);
  check(after.mastery === 95 && after.next_review_at, `树上掌握度 95、已排复习（${after.next_review_at}）`);
  check(node.mastery === 0, "对照：原快照 mastery 为 0");

  console.log(failed ? `\n${failed} 项失败` : "\n全部通过");
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
