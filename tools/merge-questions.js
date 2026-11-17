// merge-questions.js —— 把 .tmp-q/ 批次题文件校验后合并进 seed.json（例题量产滚动批次共用）
// 用法：node tools/merge-questions.js
// 校验：引用存在、类型与选项合法、stem 节点内唯一（已有题干跳过、批内重复报错）、
//       Java/语言基础 8 叶子合并后总数 ≥100；通过后按 seed.json 原格式（1 空格缩进、CRLF、无尾换行）写回
const fs = require("fs");
const path = require("path");

const root = path.join(__dirname, "..");
const seedPath = path.join(root, "seed.json");
const batchDir = path.join(root, ".tmp-q");
const batchRe = /^q\d+_[a-z]+\.json$/;
const TOPIC = "语言基础";
const MIN_PER_LEAF = 100;

const errors = [];
const seed = JSON.parse(fs.readFileSync(seedPath, "utf8"));

// 现有叶子索引与各节点已有题干
const leafKeys = new Set();
for (const sk of seed.skills)
  for (const t of sk.topics)
    for (const l of t.leaves) leafKeys.add(`${sk.name}/${t.title}/${l[0]}`);
const stemsByLeaf = new Map();
for (const q of seed.questions) {
  const k = `${q.skill}/${q.topic}/${q.leaf}`;
  if (!stemsByLeaf.has(k)) stemsByLeaf.set(k, new Set());
  stemsByLeaf.get(k).add(q.stem);
}

function validate(q, where) {
  const at = `${where}: ${String(q.stem).slice(0, 40)}`;
  if (typeof q.skill !== "string" || typeof q.topic !== "string" || typeof q.leaf !== "string")
    return errors.push(`${at} — skill/topic/leaf 缺失`);
  const key = `${q.skill}/${q.topic}/${q.leaf}`;
  if (!leafKeys.has(key)) return errors.push(`${at} — 引用不存在的叶子 ${key}`);
  if (typeof q.stem !== "string" || !q.stem.trim()) return errors.push(`${where} — stem 为空`);
  if (q.stem.includes("\n")) return errors.push(`${at} — stem 含换行`);
  if (typeof q.explanation !== "string" || !q.explanation.trim())
    return errors.push(`${at} — explanation 为空`);
  if (q.type === "choice") {
    if (!Array.isArray(q.options) || q.options.length !== 4 || q.options.some((o) => !o || typeof o !== "string"))
      return errors.push(`${at} — choice 需 4 个非空选项`);
    if (!["A", "B", "C", "D"].includes(q.answer)) return errors.push(`${at} — choice 答案须为 A-D`);
  } else if (q.type === "judge") {
    if (JSON.stringify(q.options) !== JSON.stringify(["对", "错"]))
      return errors.push(`${at} — judge 选项须为 ["对","错"]`);
    if (!["对", "错"].includes(q.answer)) return errors.push(`${at} — judge 答案须为 对/错`);
  } else {
    return errors.push(`${at} — 非法 type ${q.type}`);
  }
}

// 读取批次文件
const files = fs.existsSync(batchDir)
  ? fs.readdirSync(batchDir).filter((f) => batchRe.test(f)).sort()
  : [];
if (files.length === 0) {
  console.error(`未找到批次文件（${batchDir}/qN_xxx.json）`);
  process.exit(1);
}

let added = 0;
const skipped = [];
const batchStems = new Map(); // 批内去重
const letters = { A: 0, B: 0, C: 0, D: 0 };
const perLeaf = new Map();

for (const f of files) {
  const arr = JSON.parse(fs.readFileSync(path.join(batchDir, f), "utf8"));
  if (!Array.isArray(arr)) { errors.push(`${f} — 不是 JSON 数组`); continue; }
  arr.forEach((q, i) => {
    const where = `${f}[${i}]`;
    validate(q, where);
    const key = `${q.skill}/${q.topic}/${q.leaf}`;
    if (!batchStems.has(key)) batchStems.set(key, new Set());
    if (batchStems.get(key).has(q.stem)) { errors.push(`${where} — 批内 stem 重复：${q.stem}`); return; }
    batchStems.get(key).add(q.stem);
    if (stemsByLeaf.get(key)?.has(q.stem)) { skipped.push(`${key}: ${q.stem}`); return; }
    if (!stemsByLeaf.has(key)) stemsByLeaf.set(key, new Set());
    stemsByLeaf.get(key).add(q.stem);
    seed.questions.push(q);
    added++;
    if (q.type === "choice") letters[q.answer]++;
    const n = (perLeaf.get(key) || 0) + 1;
    perLeaf.set(key, n);
  });
}

// 合并后各语言基础叶子总数 ≥ MIN_PER_LEAF
const totals = new Map();
for (const q of seed.questions)
  if (q.topic === TOPIC) {
    const k = `${q.skill}/${q.topic}/${q.leaf}`;
    totals.set(k, (totals.get(k) || 0) + 1);
  }
for (const k of leafKeys) {
  if (!k.startsWith("Java/") || !k.includes(`/${TOPIC}/`)) continue;
  const n = totals.get(k) || 0;
  if (n < MIN_PER_LEAF) errors.push(`叶子总数不足：${k} = ${n} < ${MIN_PER_LEAF}`);
}

if (errors.length) {
  console.error(`校验失败（${errors.length} 处），未写入 seed.json：`);
  for (const e of errors.slice(0, 30)) console.error("  -", e);
  if (errors.length > 30) console.error(`  ... 其余 ${errors.length - 30} 条`);
  process.exit(1);
}

fs.writeFileSync(seedPath, JSON.stringify(seed, null, 1).replace(/\n/g, "\r\n"));

console.log(`合并完成：新增 ${added} 题，跳过已有 stem ${skipped.length} 题`);
for (const [k, n] of [...perLeaf].sort()) console.log(`  +${n}  ${k}`);
console.log("语言基础各叶子总数：");
for (const [k, n] of [...totals].sort()) console.log(`  ${n}  ${k}`);
const totalLetters = Object.values(letters).reduce((a, b) => a + b, 0);
console.log(`choice 答案分布：${Object.entries(letters).map(([l, c]) => `${l}=${c}(${Math.round((c / totalLetters) * 100)}%)`).join("  ")}`);
console.log(`seed.json questions 总数：${seed.questions.length}`);
