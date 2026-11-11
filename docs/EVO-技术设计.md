# EVO 技术设计

## 架构概览

```
[浏览器 SPA：static/index.html + app.js + style.css]
        ↕ fetch JSON（同源）
[后端：现行 Python FastAPI(8765) ⇄ 迁移中 Go net/http(8766)]
        ↕ sqlite3 / database/sql
[SQLite 单文件 data.db]
```

单机单人，无登录鉴权，无外部服务。迁移期双跑：Go 版与 Python 版共用同一 data.db（同 schema 只读兼容），前端同一套 static/ 资源。

## 技术栈
- 前端：原生 HTML/CSS/JS，无框架无构建
- 后端：Python FastAPI + uvicorn（现行）→ Go 1.27 标准库 net/http + modernc.org/sqlite（目标，纯 Go 无 CGO）
- 数据库：SQLite（data.db，gitignore，首次启动自动建库播种）
- 部署：start.bat（Python 版）→ go build 单 exe evo.exe（Go 版，静态资源 go:embed 内嵌）

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
| title | string | 节点名，非空 |
| evidence | string | 验证方式（怎么证明掌握） |
| order_index | integer | 同层排序 |
| mastery | real | 0-100，档位 0/30/60/80/95，默认 0 |
| review_stage | integer | 复习间隔档 0-5 → [1,2,4,7,15,30] 天 |
| last_verified_at | date? | 最后打分/复习日 |
| next_review_at | date? | 下次复习到期日 |

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
skills 1──N nodes（自引用）  skills 1──N logs  nodes 1──N logs  skills 1──N milestones
技能进度 = 该技能所有叶子节点（无子节点）mastery 平均值（1 位小数）
状态 = mastery 映射：0 / 1-59 / 60-79 / 80-94 / 95+ → 未开始/学习中/巩固中/已掌握/已精通
```

## API 设计

### 看板
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | / | 首页 SPA |
| GET | /api/health | 健康检查（Go 版已有）`{status,service}` |
| GET | /api/dashboard | 聚合：skills / today_tasks / due_reviews / milestones / recent_logs / streak / today_count |
| GET | /api/skills | 技能列表（Go 版已有，Python 版无此独立端点） |

### 技能树
| 方法 | 路径 | 说明 |
|---|---|---|
| GET | /api/skills/{id}/tree | 节点列表（含 status、next_review_at） |
| POST | /api/nodes | 添加节点 `{skill_id, parent_id?, title, evidence?}` |
| DELETE | /api/nodes/{id} | 删除节点及其子节点 |

### 掌握度（唯一状态推进入口）
| 方法 | 路径 | 说明 |
|---|---|---|
| POST | /api/nodes/{id}/score | 打分 `{mastery, evidence?}` → 安排复习、证据写日志 |
| POST | /api/nodes/{id}/review | 自测 `{score}` → ≥80 升档+5 / 60-79 维持 / <60 归档-15 |

### 日志与里程碑
| 方法 | 路径 | 说明 |
|---|---|---|
| GET / POST | /api/logs | 列表（默认 50）/ 写入 `{content, skill_id?, node_id?, type?}` |
| POST | /api/milestones/{id} | 勾选/取消 `{done}` |

## 模块依赖

```
main.go（路由注册 + 启动）
  ├── db.go（连接管理、查询函数、handler）──── modernc.org/sqlite（driver）
  ├── （待建）dashboard.go（M3 聚合查询）
  ├── （待建）seed.go（M3.5 建表播种，参照 seed.py）
  ├── （待建）handlers.go（M4 其余 API）
  └── static/（M5 经 go:embed 内嵌）
```

Python 版对应关系：server.py（路由）→ db.py（数据层+调度算法）→ seed.py（种子）。

**今日任务算法**（db.py `today_tasks`，Go 需一致实现）：
1. 复习到期（next_review_at ≤ 今天，按逾期天数降序）取满
2. 不足补：mastery ∈ (0,80) 且未到期，按 mastery 升序
3. 仍不足补：总进度最低技能的第一个未开始叶子
共 3 条，标注 reason：复习到期 / 继续推进 / 新内容

**连续天数**：logs 中不同日期倒序连数（今天没有则从昨天起算，断为 0）。

## 技术约束
- 掌握度只能通过 score 接口提升且必须附证据（证据入日志）；复习调度区间固定 [1,2,4,7,15,30]
- Go 版不得改动 data.db 表结构；迁移期与 Python 版共存，同 schema
- 缺 data.db 时 SQLite 会自动建**空文件**——Go 版必须实现 seed 播种（M3.5）否则新环境得到空系统
- 前端零框架零构建；界面中文；文件 UTF-8；data.db、*.exe 不入库
- Go 版错误处理：handler 内查询/编码失败返回 500 + 错误文本，不静默吞错

## 第三方集成
- 无外部服务。Go 侧唯一第三方依赖 modernc.org/sqlite（纯 Go SQLite 驱动，经 goproxy.cn 获取）
