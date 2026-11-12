# EVO 技术设计

## 架构概览

```
[浏览器 SPA：static/index.html + app.js + style.css]（go:embed 编译进 exe）
        ↕ fetch JSON（同源 127.0.0.1:8765）
[后端：Go evo.exe（net/http + 路由 + goldmark 课文渲染）] ← Python FastAPI 保留为回滚路径
        ↕ database/sql（modernc.org/sqlite，纯 Go）
[数据根 D:\CloudDriver\EVO（evo.json 配置，可远程同步）]
  ├── data.db                # 进度/日志/作答/配置（唯一不可再生）
  ├── <技能>/<主题>/<知识点>.md  # 教课文（content_dirs 相对路径解析）
  └── backup/evo-*.db        # VACUUM INTO 一致性快照
```

单机单人，无登录鉴权，无外部服务。**仓库与数据分离**：代码/文档/seed.json 走 GitHub，数据根走用户同步盘，evo.json（exe 旁）声明数据根路径，Go 与 Python 双版读同一配置。

## 技术栈
- 前端：原生 HTML/CSS/JS，无框架无构建（编译进 exe）
- 后端：Go 1.27 标准库 net/http + modernc.org/sqlite（纯 Go 无 CGO）+ goldmark（md→HTML，GFM）
- 数据库：SQLite，位于数据根；首次启动自动建库播种、老库自动迁移（ALTER 补列/回填）
- 部署：start.bat → 单 exe evo.exe（go:embed 内嵌 static/ 与 seed.json）
- 配置：evo.json（`data_dir` 数据根路径；exe 目录优先，回落工作目录）

## 数据模型

### skills（技能）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| name | string | Java/Go/英语/高数，非空 |
| color | string | 看板主题色 hex |
| description | string | 技能描述 |

### nodes（技能树节点，自引用两层：主题 → 知识点）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| skill_id | integer FK→skills | 所属技能 |
| parent_id | integer? FK→nodes | NULL = 顶层主题 |
| title | string | 节点名，非空；含 / * ? 等字符时课文文件名全角化 |
| evidence | string | 验证方式（怎么证明掌握） |
| content | string | 知识点卡片摘要（概念/要点/易错短文，课文为空时回落显示） |
| order_index | integer | 同层排序 |
| mastery | real | 0-100，档位 0/30/60/80/95，默认 0 |
| review_stage | integer | 复习间隔档 0-5 → [1,2,4,7,15,30] 天 |
| last_verified_at | date? | 最后打分/复习日 |
| next_review_at | date? | 下次复习到期日 |

### questions（例题，N:1 挂知识点）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| node_id | integer FK→nodes | 归属知识点 |
| type | string | choice 单选 / judge 判断（自动判分） |
| stem | string | 题干 |
| options | string | 选项 JSON 数组（紧凑格式，双版字节一致） |
| answer | string | 标准答案（choice="A"，judge="对"/"错"） |
| explanation | string | 解析 |
| order_index | integer | 题序（来自 seed.json 顺序） |

### answers（作答记录，每次一条）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| question_id | integer FK→questions | 题目 |
| node_id | integer FK→nodes | 冗余节点，按知识点统计免联表 |
| answered_at | datetime | 作答时间 |
| user_answer | string | 用户作答 |
| correct | integer | 0/1 |

### settings（键值配置）
| 字段 | 类型 | 说明 |
|---|---|---|
| key | string PK | 如 content_dirs |
| value | string | JSON 数组（内容目录列表，数据根内存相对路径 "."） |

### logs（学习日志）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| created_at | datetime | ISO 秒精度，非空 |
| skill_id / node_id | integer? | 关联技能/节点，可空 |
| type | enum | study / review / project |
| content | string | 正文，非空 |

### milestones（里程碑）
| 字段 | 类型 | 说明 |
|---|---|---|
| id | integer PK | 自增 |
| skill_id | integer FK→skills | 所属技能 |
| title | string | 描述，非空 |
| done | bool | 勾选时自动写 project 日志 |

### 关系与派生
```
skills 1──N nodes（自引用）  nodes 1──N questions  questions 1──N answers
skills 1──N logs  nodes 1──N logs  skills 1──N milestones
技能进度 = 该技能所有叶子节点（无子节点）mastery 平均值（1 位小数）
状态 = mastery 映射：0 / 1-59 / 60-79 / 80-94 / 95+ → 未开始/学习中/巩固中/已掌握/已精通
课文文件 = <内容目录>/<技能>/<主题>/<标题全角化>.md（goldmark 渲染，现读现渲染）
```

## API 设计

### 看板
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | / | 首页 SPA |
| GET | /api/health | 健康检查 `{status,service}` |
| GET | /api/dashboard | 聚合：skills / today_tasks / due_reviews / milestones / recent_logs / streak / today_count |
| GET | /api/skills | 技能列表 |

### 技能树
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/skills/{id}/tree | 节点列表（含 status、question_count、has_markdown、content） |
| POST | /api/nodes | 添加节点 `{skill_id, parent_id?, title, evidence?}`（点路径标题拒绝） |
| DELETE | /api/nodes/{id} | 删除节点及其子节点 |

### 掌握度（唯一状态推进入口）
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /api/nodes/{id}/score | 打分 `{mastery, evidence?}` → 安排复习、证据写日志 |
| POST | /api/nodes/{id}/review | 自测 `{score}` → ≥80 升档+5 / 60-79 维持 / <60 归档-15 |

### 例题
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/nodes/{id}/questions | 题目列表 + 每题最近作答 + 节点统计 `{attempted, correct}` |
| POST | /api/questions/{id}/answer | 提交 `{user_answer}` → 自动判分、落 answers、返回最新统计 |

### 课文与配置
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/nodes/{id}/markdown | `{found, path, markdown, html}`（goldmark 渲染） |
| GET/POST/DELETE | /api/settings/content-dirs | 列表（含 data_dir 与每目录课文数）/ 添加（422 校验、409 去重）/ 移除 |
| POST | /api/backup | VACUUM INTO 快照到 数据根/backup/（运行中安全） |

### 日志与里程碑
| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | /api/logs | 列表（默认 50）/ 写入 `{content, skill_id?, node_id?, type?}` |
| POST | /api/milestones/{id} | 勾选/取消 `{done}` |

## 模块依赖

```
main.go（路由注册 + 启动 + 静态/兜底）
  ├── db.go（evo.json 数据根加载、连接、skills 查询）
  ├── dashboard.go（聚合：进度/今日任务/复习队列/连续天数）
  ├── api.go（技能树/节点/打分/复习/例题/日志/里程碑 + 405/404/422 响应件）
  ├── content.go（内容目录管理、路径相对化解析、md 加载与 goldmark 渲染、快照备份）
  ├── seed.go（DDL + 老库迁移 ALTER + seed.json 播种/回填，go:embed）
  └── static/（go:embed）
依赖：modernc.org/sqlite（driver）、github.com/yuin/goldmark（+GFM 扩展）
```

Python 回滚路径：server.py（路由）→ db.py（数据层+调度，读同一 evo.json）→ seed.py（同一 seed.json）。

**今日任务算法**（双版一致）：
1. 复习到期（next_review_at ≤ 今天，按逾期天数降序）取满
2. 不足补：mastery ∈ (0,80) 且未到期，按 mastery 升序（seen 为空时不拼 NOT IN 子句）
3. 仍不足补：总进度最低技能的第一个未开始叶子
共 3 条，标注 reason：复习到期 / 继续推进 / 新内容

**连续天数**：logs 中不同日期倒序连数（今天没有则从昨天起算，断为 0）。

## 技术约束
- 掌握度只能通过 score 接口提升且必须附证据（证据入日志）；复习调度区间固定 [1,2,4,7,15,30]
- Go 版不得改动既有表结构；新增表（questions/answers/settings）为加法，Python 回滚可忽略
- 缺 data.db 时 SQLite 自动建空库——seed 播种兜底；老库启动时自动补列/回填（content、questions）
- **seed.json 与 static/ 是编译期嵌入**：改例题/前端必须重新 `go build`；课文 md 是运行期现读，改文件即时生效
- 课文文件名映射：Windows 非法字符（/ \ : * ? " < > |）→ 全角同形字符，标题与解析两端同一规则
- 数据根内 content_dirs 存相对路径（换机器/同步不丢）；数据根外目录存绝对路径
- 前端零框架零构建；界面中文；文件 UTF-8；data.db、*.exe、*~ 不入库
- 错误处理：handler 内失败返回 500 + 错误文本；404/405/422 对齐 FastAPI 形状 `{"detail": ...}`

## 第三方集成
- 无外部服务。Go 依赖：modernc.org/sqlite（纯 Go 驱动）、github.com/yuin/goldmark（Markdown 渲染），均经 goproxy.cn 获取
