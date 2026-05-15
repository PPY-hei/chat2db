# Chat2DB Web

> 一个轻量、Web 化的 **PostgreSQL / MySQL** 可视化管理平台，支持多人协作、基于角色的 SQL 权限拦截、团队共享 AI 辅助写 SQL。后端 Go + Gin + GORM，前端 React + AntD + Monaco。

UI 参考了 VSCode 的树形结构与 Navicat 的表数据浏览，定位是**可以丢在内网给团队使用的"数据库管理协作台"**：

- 每个连接分配到一个"连接组"，组 = 共享单元。
- 账号之间不共用数据库账号，权限完全由**应用层 RBAC + SQL 解析器**控制，杜绝"Viewer 执行 DROP"这种越权。
- 支持 AI 对话写 SQL，组 Owner 可共享自己的 LLM 配置给组内成员，无需每人一份 API Key。
- 支持 **SSH 隧道** 与 **双向 SSL 认证**，敏感凭据（DB 密码、SSH 密码/私钥/Passphrase、证书私钥）均做 AES-GCM 加密落库。

## 界面预览

### 添加 PostgreSQL 连接

每个连接归属一个组，密码会在落库前做 AES-GCM 加密；支持测试连接、多种 SSL 模式、SSH 隧道（密码 / 私钥 + Passphrase）以及自定义 CA / 客户端证书的双向 TLS。

![添加 PG 连接](docs/screenshots/add-pg-connection.jpg)

### 添加 MySQL 连接

同一套连接表单支持驱动切换，MySQL 默认端口 3306，SSL 模式自动收敛到 `disable / require / verify-ca`，其余字段（SSH 隧道、证书上传）与 PG 完全一致。

![添加 MySQL 连接](docs/screenshots/mysql-connect.jpg)

### 连接组管理：成员 / 角色 / LLM 共享

Owner 可邀请成员并指派 Owner / Editor / Viewer；Editor 可继续邀请 Viewer / Editor。Owner 还能一键打开 **AI 大模型配置共享**，让组员复用 Owner 的 LLM 凭据，无需每人配置一遍 API Key。

![组设置](docs/screenshots/group-settings.jpg)

### 表数据浏览：VSCode 风格左树 + Navicat 风格表格

左侧树按 连接组 → 连接 → 数据库 → Schema → 表/视图 懒加载展开；右侧 Tab 支持分页、列头三态排序、真实 COUNT、查看 DDL、在 SQL 窗口打开，以及单元格内联编辑（带权限校验，主键感知 UPDATE 生成）。

![表数据](docs/screenshots/tables.jpg?v=2)

### 多条件筛选：contains / IN / IS NULL / bool 下拉

列头点击即可叠加多条筛选：字符串列支持 `contains`、`IN`；数值/日期列支持比较与范围；`nullable` 列支持 `IS NULL / IS NOT NULL`；`bool` 列直接下拉；组合后的条件会实时拼回 WHERE 并重新 COUNT。

![多条件筛选](docs/screenshots/filter.jpg)

### AI 写 SQL：`@` 引用表自动注入 DDL + 选区行号引用

在 AI 对话框输入 `@` 可补全当前连接里的所有表，提交时后端会自动把被引用表的 DDL 作为上下文一并发送给模型。编辑器里已选中的 SQL 片段会作为"编辑器引用"显式展示在弹窗顶部，包含 **第 X – Y 行** 行号范围、字符/行数统计、可展开的片段预览、一键定位到编辑器或移除引用。返回的 SQL 可直接插入到光标位置。

![AI 写 SQL](docs/screenshots/ai-sql.jpg)

## 目录

- [Chat2DB Web](#chat2db-web)
  - [界面预览](#界面预览)
  - [目录](#目录)
  - [功能总览](#功能总览)
  - [技术栈与架构](#技术栈与架构)
    - [后端](#后端)
    - [前端](#前端)
    - [整体架构图](#整体架构图)
  - [目录结构](#目录结构)
  - [权限模型](#权限模型)
  - [SQL 解析器与安全](#sql-解析器与安全)
  - [本地开发](#本地开发)
    - [先决条件](#先决条件)
    - [后端](#后端-1)
    - [前端](#前端-1)
  - [生产部署](#生产部署)
    - [方式 A：二进制 + 静态托管](#方式-a二进制--静态托管)
    - [方式 B：Docker](#方式-bdocker)
    - [反向代理（Nginx 示例）](#反向代理nginx-示例)
  - [环境变量](#环境变量)
  - [API 速查](#api-速查)
  - [默认快捷键](#默认快捷键)
  - [数据与安全性说明](#数据与安全性说明)
  - [路线图 / TODO](#路线图--todo)
  - [License](#license)

## 功能总览

| 分类 | 功能 |
|------|------|
| 账号 | 邮箱 + 密码注册、登录（bcrypt），JWT 鉴权 |
| 连接组 | Owner / Editor / Viewer 三级 RBAC；Owner 可把组分享给其他账号；Editor 可邀请 Viewer/Editor 成员；Owner 可切换"大模型配置共享" |
| 数据源 | **PostgreSQL**（`pgx` 池化）与 **MySQL**（`database/sql` + `go-sql-driver/mysql` 池化）；按连接缓存，编辑 / 删除 / 更新凭据时自动失效 |
| 安全通道 | **SSH 隧道**（密码 / 私钥 + Passphrase 认证）、**双向 SSL** 连接（自定义 CA / 客户端证书 / 客户端私钥）；所有敏感凭据 AES-256-GCM 加密落库 |
| 浏览 | VSCode 风格左侧树：连接组 → 连接 → 数据库 → Schema → 表/视图；会话级别**数据库切换**，无需改连接即可跨库浏览；懒加载 + 虚拟滚动；支持按组刷新 Schema |
| 表数据 | 分页（20/50/100/200/500）、列头三态排序、多条件筛选（含 `contains`、`IN`、`IS NULL`、bool 列专用下拉）、COUNT(*) 真实总数、`查看 DDL`、`在 SQL 窗口打开`、**单元格内联编辑**（带权限校验 + 主键感知 UPDATE 生成） |
| SQL 窗口 | Monaco 编辑器、多 Tab、拖拽调整 SQL/结果区比例、选中执行、结果复制 TSV / 导出 CSV；方言随连接自动切换（postgres / mysql） |
| SQL 权限拦截 | 后端 SQL 解析器按语句分类（read/write/ddl/admin/tx）匹配角色白名单，支持多语句、注释、字符串、`$tag$` dollar-quoted、CTE 写操作保守策略 |
| AI 写 SQL | OpenAI 兼容接口，任何 endpoint 可接；输入框 `@` 引用当前连接的表，自动把引用表 DDL 一起发送给模型；引用以 Tag 展示并可删除；方言随连接切换；Owner 可把 LLM 配置共享给组内成员 |
| 团队共享 SQL | 任意组员可收藏 SQL 到组内（标题必填、描述可选）；组内所有成员可见、可"插入到光标"；个人收藏视图聚合所有组内收藏，一键跳转执行 |
| UX | 侧边栏宽度可拖拽、SQL 编辑器/结果区比例可拖拽、尺寸持久化到 localStorage；树结构虚拟化避免大表性能问题 |

## 技术栈与架构

### 后端

- Go 1.25
- [Gin](https://github.com/gin-gonic/gin) HTTP 框架
- [GORM](https://gorm.io) + SQLite 作为**应用元数据库**（账号 / 组 / 成员 / 连接 / 收藏 SQL）
- [pgx v5](https://github.com/jackc/pgx) 连接目标 PostgreSQL 实例（池化，每连接独立池）
- [go-sql-driver/mysql](https://github.com/go-sql-driver/mysql) 连接目标 MySQL 实例（`database/sql` 池化，每连接独立池）
- [golang.org/x/crypto/ssh](https://pkg.go.dev/golang.org/x/crypto/ssh) 建立 SSH 隧道并做本地端口转发
- [golang-jwt](https://github.com/golang-jwt/jwt) JWT 签发/校验
- `golang.org/x/crypto/bcrypt` 账号密码
- `crypto/aes`（AES-256-GCM）加密数据库密码 / SSH 凭据 / 证书 / LLM API Key
- 内置 SQL 解析器（`internal/sqlguard`），纯 Go 实现无额外依赖

### 前端

- React 18 + TypeScript
- [Vite 5](https://vitejs.dev) 构建 / Dev Server
- [Ant Design 5](https://ant.design)（中文 locale）
- [Monaco Editor](https://microsoft.github.io/monaco-editor/) （`@monaco-editor/react`） —— SQL 编辑器
- [React Router v6](https://reactrouter.com)
- [Zustand](https://github.com/pmndrs/zustand) 轻量状态
- [axios](https://axios-http.com)（401 自动跳转登录）

### 整体架构图

```
┌─────────────────────────────────────────────────────────────────┐
│                          Browser (SPA)                          │
│  React + AntD + Monaco   │  axios (/api, 自带 JWT)              │
└─────────────────────────────────────────────────────────────────┘
                               │ HTTPS
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│                       Go Backend (Gin)                          │
│  ┌─────────┬──────────┬─────────────────────────┐               │
│  │ auth    │ middleware│ api/handlers + router  │               │
│  │ (JWT)   │ (AuthReq) │                         │              │
│  ├─────────┴──────────┴─────────────────────────┤               │
│  │ service (user/group/connection/saved_query)  │               │
│  ├──────────────────┬────────────────────────────┤              │
│  │ sqlguard         │ dbexec (pool per conn)    │               │
│  │ (permission)     │ pg / mysql / meta / DDL   │               │
│  ├──────────────────┼────────────────────────────┤              │
│  │ llm.Chat         │  crypto (AES-GCM)         │               │
│  │ (OpenAI compat)  │  ssh tunnel (optional)    │               │
│  └──────────────────┴────────────────────────────┘              │
│  GORM + SQLite (app metadata) ───► chat2db.db                   │
└─────────────────────────────────────────────────────────────────┘
        │                       │                     │
        ▼                       ▼                     ▼
 PostgreSQL / MySQL 实例  可选 SSH 堡垒机 + SSL   OpenAI 兼容 API
 （组成员共享）           （组成员共享）         （可选、用户级/组共享）
```

## 目录结构

```
chat2db/
├── server/                         # Go 后端
│   ├── cmd/server/main.go          # 启动入口
│   ├── .env.example
│   └── internal/
│       ├── api/                    # 路由 + HTTP handlers
│       ├── auth/                   # JWT 签发 / 解析
│       ├── config/                 # env 读取
│       ├── crypto/                 # AES-GCM
│       ├── db/                     # SQLite 初始化 + AutoMigrate
│       ├── dbexec/                 # 连接池 + 元数据 + DDL 生成
│       │   ├── pg.go / pg_meta.go          # PostgreSQL (pgx)
│       │   ├── mysql.go / mysql_meta.go    # MySQL (database/sql)
│       │   ├── meta.go                     # 驱动分发 / 临时 database 切换
│       │   └── ssh.go                      # SSH 隧道（复用 / 失效管理）
│       ├── llm/                    # OpenAI 兼容代理 + 凭据回退
│       ├── middleware/             # AuthRequired
│       ├── model/                  # User/Group/GroupMember/Connection/SavedQuery
│       ├── service/                # 业务逻辑
│       └── sqlguard/               # SQL 解析 + 权限拦截（含单测）
└── web/                            # React 前端
    ├── vite.config.ts              # /api 代理到 :8080
    └── src/
        ├── api.ts                  # axios 封装
        ├── store.ts                # zustand auth
        ├── types.ts                # 与后端对齐的 TS 类型
        ├── pages/
        │   ├── LoginPage.tsx
        │   └── MainLayout.tsx      # Header + 可拖拽侧边栏 + 多 Tab
        └── components/
            ├── ConnectionFormModal.tsx
            ├── GroupManageModal.tsx   # 成员管理 + ShareLLM 开关
            ├── LLMConfigModal.tsx     # 个人 LLM 配置
            ├── MySavedQueriesModal.tsx
            ├── SQLTab.tsx             # Monaco + AI @ 引用 + 收藏
            └── TableDataTab.tsx       # 分页 + 排序 + 筛选 + DDL
```

## 权限模型

```
User ─┬──(多对多 via GroupMember)──► Group ─┬──► Connection (多对一)
      │                                      └──► SavedQuery  (多对一)
      │
      └──► 可选 LLM 配置（endpoint / model / apiKey 加密）
```

角色权限矩阵：

| 动作 | Viewer | Editor | Admin | Owner |
|------|:------:|:------:|:------:|:------:|
| 查看连接 / Schema / 表 | ✓ | ✓ | ✓ | ✓ |
| 执行 SELECT / SHOW / EXPLAIN | ✓ | ✓ | ✓ | ✓ |
| 执行 INSERT / UPDATE / DELETE / MERGE | ✗ | ✓ | ✓ | ✓ |
| 执行 BEGIN / COMMIT / ROLLBACK | ✗ | ✓ | ✓ | ✓ |
| 执行 CREATE / ALTER / DROP / TRUNCATE | ✗ | ✗ | ✓ | ✓ |
| 执行 GRANT / REVOKE / VACUUM / ANALYZE | ✗ | ✗ | ✓ | ✓ |
| 查看审计日志（按组隔离） | ✗ | ✗ | ✓ | ✓ |
| 邀请新成员（viewer / editor） | ✗ | ✓ | ✓ | ✓ |
| 邀请 / 提升为 Admin / Owner | ✗ | ✗ | ✗ | ✓ |
| 修改既有成员角色 / 移除成员 | ✗ | ✗ | ✗ | ✓ |
| 新建 / 编辑 / 删除连接 | ✗ | ✗ | ✗ | ✓ |
| 切换 ShareLLM / 改组名描述 | ✗ | ✗ | ✗ | ✓ |
| 收藏 SQL | ✓ | ✓ | ✓ | ✓ |

> Admin 在 SQL 层等同 Owner（可执行任意 DDL / 管理语句），但**组管理**仍是 Owner 专属：
> Admin 不能修改既有成员角色、不能邀请 Owner / Admin、不能动连接 CRUD 与组元信息。

## SQL 解析器与安全

`internal/sqlguard` 是纯 Go 无外部依赖的轻量 SQL 分类器，解决三个问题：

1. **多语句切分**：能正确识别字符串、`--` 行注释、`/* */` 块注释、`$tag$...$tag$` dollar-quoted，不把里面的 `;` 当作语句分隔符。
2. **按首关键字分类**：
   - `read`：SELECT / SHOW / EXPLAIN / VALUES / WITH ... SELECT ...
   - `write`：INSERT / UPDATE / DELETE / MERGE / UPSERT / COPY / WITH ... 含 DML
   - `ddl`：CREATE / ALTER / DROP / TRUNCATE / RENAME / COMMENT
   - `tx`：BEGIN / COMMIT / ROLLBACK / SAVEPOINT
   - `admin`：GRANT / REVOKE / VACUUM / ANALYZE / REINDEX / SET / RESET / LISTEN
3. **按角色白名单放行**：在 handler 最开始拦截，任意一句越权整个请求都不放行。

CTE 对 `WITH x AS (...) INSERT/UPDATE/DELETE ...` 采用**保守策略**：只要 CTE 内含任意 DML 关键字，整体按 write 分类。详见 `sqlguard_test.go`。

其它安全实践：

- 数据库密码 / SSH 密码 / SSH 私钥 / SSH Passphrase / 客户端证书私钥 / LLM API Key 全部用 AES-256-GCM（32 字节 key）加密落 SQLite。
- pgx 与 `database/sql` 查询统一走参数化（元数据查询如 `ListTables` / `ListColumns` 用占位符），用户层 SQL 走 SQL 解析器白名单。
- 支持 SSL 模式 `disable` / `require` / `verify-ca` / `verify-full`（PG 全部支持，MySQL 支持 `disable` / `require` / `verify-ca`）；上传的 CA / 客户端证书 / 客户端私钥经解密后组装成 `*tls.Config` 传给底层驱动。
- 可选的 SSH 隧道仅在**进程内存**维护 `connID → listener + ssh.Client` 的复用条目，连接更新/删除时立刻失效并关闭。
- JWT 通过 HS256 签发；未带/过期 token 返回 401，前端全局拦截器会自动跳回登录页。
- LLM API Key 永远只驻留在后端内存，通过服务端代理转发，不会在 `/api/me` 等接口返回给前端。

## 审计日志

后端在以下事件点埋点，进入异步 worker → MySQL/PostgreSQL/SQLite 的 `audit_logs` 表：

| 事件 | Action | 关键字段 |
|------|--------|----------|
| SQL 执行（含被 sqlguard 拦截的尝试） | `sql.execute` | `detail.sql / role / failed_sql` |
| 登录成功 / 失败 / 注册 | `auth.login.success` / `auth.login.fail` / `auth.register` | `user_email / ip / ua` |
| 邀请 / 更新 / 移除成员 | `member.add` / `member.update` / `member.remove` | `target=邮箱 / detail.role` |
| 连接 CRUD / 测试 | `connection.create` / `update` / `delete` / `test` | `target=连接名 / detail.host:port` |

设计要点：
- **异步入库**：业务路径调用 `service.LogAudit`，进入容量 1024 的 buffered channel，由后台 goroutine 顺序写入，不阻塞用户请求；队列满时丢弃并打印 warn，可通过 `service.AuditDroppedTotal()` 观察累计丢弃。
- **保留策略**：默认保留 90 天，每 6 小时清理一次旧记录。可通过 `AUDIT_RETENTION` 环境变量调整（如 `AUDIT_RETENTION=720h` 为 30 天，`<=0` 表示永不清理）。
- **可见性按组隔离**：`GET /api/audit/logs` 仅允许在任一组是 admin/owner 的用户访问；查询 SQL 自动按调用者作为 admin/owner 的组列表过滤，并叠加自身的无组事件（auth.*）。Admin / Owner 之间互不可见对方组的事件。
- **前端入口**：`MainLayout` 头部出现"审计日志"按钮（仅当用户在任意组是 admin/owner 时显示）；时间窗口默认近 7 天，支持改时间、过滤 action / 组 / 关键字 / 仅失败、按行展开 detail JSON。
- **敏感字段**：当前按 PRD 要求**原样存储** SQL 文本与 client IP / UA。如有合规需求，可在 `service.LogAudit` 入口加截断 / 哈希钩子。

## 本地开发

### 先决条件

- Go ≥ 1.25
- Node ≥ 18
- 一套可访问的 **PostgreSQL（10+）或 MySQL（5.7+ / 8.x）实例**
- 可选：OpenSSH 堡垒机（用于 SSH 隧道）、CA / 客户端证书（用于 verify-ca / verify-full）
- 可选：OpenAI 兼容 API（OpenAI / DeepSeek / Kimi / Qwen / 本地 vLLM 等）

### 后端

```bash
cd server
cp .env.example .env        # 修改 JWT_SECRET / CREDENTIAL_KEY（必须 ≥ 32 字节）
go run ./cmd/server
# 默认监听 :8080，SQLite 自动创建在 ./data/chat2db.db
```

也可以用 `go build`：

```bash
go build -o bin/chat2db-server ./cmd/server
./bin/chat2db-server
```

跑单测：

```bash
go test ./... -race -count=1
```

### 前端

```bash
cd web
npm install
npm run dev
# 打开 http://localhost:5173，注册即用
```

`vite.config.ts` 已把 `/api` 代理到 `http://localhost:8080`。

生产构建：

```bash
npm run build           # 产物在 web/dist
npm run preview         # 本地预览构建结果
```

## 生产部署

### 选择元数据库驱动

应用自身的数据（账号、组、连接、收藏 SQL 等）**不等于**你要管理的业务库，它有自己的元数据库。启动时通过 `META_DB_DRIVER` 选择：

| Driver | 适合场景 | 说明 |
|--------|----------|------|
| `sqlite` | 单机 / 内网 / 小团队，零配置优先 | 走 GORM AutoMigrate，无需迁移工具；文件落在 `META_DB_DSN` 指定路径 |
| `postgres` | 多实例部署 / 审计量大 / 企业治理 | 启动时用 golang-migrate 执行内嵌 SQL（`internal/db/migrations/postgres/`） |
| `mysql` | 与现有 MySQL 基础设施统一 | 同上，migration 位于 `internal/db/migrations/mysql/`，migration 期间短连接带 `multiStatements=true`，业务连接池不启用该参数 |

常见启动方式：

```bash
# SQLite（默认，零配置）
META_DB_DRIVER=sqlite \
META_DB_DSN=./data/chat2db.db \
./chat2db-server

# PostgreSQL
META_DB_DRIVER=postgres \
META_DB_DSN="postgres://chat2db:chat2db@pg-host:5432/chat2db?sslmode=disable" \
./chat2db-server

# MySQL（注意 parseTime=true）
META_DB_DRIVER=mysql \
META_DB_DSN="chat2db:chat2db@tcp(mysql-host:3306)/chat2db?parseTime=true&loc=Local&charset=utf8mb4" \
./chat2db-server
```

本地想快速拉起一个 PG / MySQL 用来联调：

```bash
# Docker Compose 自带 PG / MySQL 两个 profile，默认什么都不启
docker compose --profile postgres up -d
docker compose --profile mysql    up -d

# 然后在本机直接 go run ./cmd/server，并把 META_DB_DSN 指向容器
```

### Migration 策略

- **sqlite**：`META_DB_AUTO_MIGRATE=true`（默认）走 GORM AutoMigrate，无需额外步骤。
- **postgres / mysql**：`META_DB_AUTO_MIGRATE=true`（默认）会在启动时执行 `internal/db/migrations/<driver>/` 下的迁移文件，通过 `schema_migrations` 表幂等。如果企业流程要求把迁移交给 DBA，设 `META_DB_AUTO_MIGRATE=false`，启动时应用只校验五张核心表是否存在，不存在直接 fatal。
- 任何一次对 `server/internal/model/model.go` 的改动，都必须同步在 `migrations/postgres` 与 `migrations/mysql` 各写一对 `NNNNNN_xxx.up.sql` / `.down.sql`。
- 若 migration 中途失败（`Dirty database version ...`），按 golang-migrate 标准流程：`migrate -path ... -database ... force <last-good-version>`，然后修正 SQL 重跑。

### 从 SQLite 迁移到 PG / MySQL（一次性手册骨架）

正式一键工具不在当前版本提供，推荐以下手工流程：

1. **停机**：下掉 chat2db 进程，避免迁移中途有新数据写入。
2. **备份**：拷贝 `META_DB_PATH` 对应的 `chat2db.db` 文件。
3. **准备目标库**：创建空的 PG/MySQL database（按上一节 DSN 里的名字）。
4. **应用 schema**：临时用 `META_DB_DRIVER=postgres|mysql` 启动一次 chat2db 让它 migrate up，或直接用 `golang-migrate` CLI 执行 `internal/db/migrations/<driver>/`。
5. **数据搬迁**：使用 `sqlite3 chat2db.db .dump` 导出，做列名/类型方言替换（BOOLEAN、时间字段等），再 `psql` / `mysql` 导入；或写一个 Go 小脚本读 SQLite 逐表写入目标库。
6. **校验**：对比 `SELECT COUNT(*)`、抽样对比 `users / groups / connections / saved_queries`；尝试用原账号登录。
7. **切换**：修改 chat2db 启动的 `META_DB_DRIVER / META_DB_DSN`，重新拉起。
8. **灰度观察**：保留 SQLite 文件至少一个保留周期再删除。

> ⚠️ 注意：SQLite 与 PG/MySQL 的时间精度和布尔类型表示不同，迁移数据时务必显式处理；`CREDENTIAL_KEY` **不要**改变，否则所有加密字段都会解不开。

### 方式 A：二进制 + 静态托管

```bash
# 1. 构建
cd server && go build -o ../bin/chat2db-server ./cmd/server
cd ../web && npm ci && npm run build

# 2. 部署
# - bin/chat2db-server 丢到服务器，放进 systemd 或 supervisor
# - web/dist 丢到 nginx / caddy / s3 等静态托管
```

`systemd` 示例（`/etc/systemd/system/chat2db.service`）：

```ini
[Unit]
Description=Chat2DB Web Server
After=network.target

[Service]
User=chat2db
WorkingDirectory=/opt/chat2db
EnvironmentFile=/opt/chat2db/.env
ExecStart=/opt/chat2db/chat2db-server
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

### 方式 B：Docker

项目暂未附带官方 Dockerfile，可以用下面的模板（多阶段构建）：

```dockerfile
# ---- Backend ----
FROM golang:1.25-alpine AS backend-builder
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/server ./cmd/server
# SQLite 驱动需要 CGO，alpine 下需要 gcc/musl-dev：
# RUN apk add --no-cache gcc musl-dev

# ---- Frontend ----
FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# ---- Runtime ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=backend-builder /out/server /app/server
COPY --from=web-builder /web/dist /app/public
EXPOSE 8080
ENTRYPOINT ["/app/server"]
```

注意：**服务端当前没有内置静态文件托管**，如果想让 Go 进程同时 serve 前端，需要自己在 `api/router.go` 里加一个 `r.Static("/", "./public")` 路由；或直接用 Nginx/Caddy 分开托管。

### 反向代理（Nginx 示例）

```nginx
server {
    listen 80;
    server_name chat2db.example.com;

    root /var/www/chat2db/dist;
    index index.html;

    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 60s;
    }

    # SPA fallback
    location / {
        try_files $uri $uri/ /index.html;
    }
}
```

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `SERVER_ADDR` | `:8080` | 监听地址 |
| `SERVER_MODE` | `debug` | `debug` / `release`（对应 gin 模式）。`release` 下若 `JWT_SECRET` / `CREDENTIAL_KEY` 仍为默认占位值，启动即失败 |
| `META_DB_DRIVER` | `sqlite` | 元数据库驱动：`sqlite` / `postgres` / `mysql` |
| `META_DB_DSN` | `./data/chat2db.db`（仅 sqlite 缺省） | 元数据库连接串。PG 形如 `postgres://user:pass@host:5432/db?sslmode=disable`，MySQL 形如 `user:pass@tcp(host:3306)/db?parseTime=true&loc=Local&charset=utf8mb4` |
| `META_DB_PATH` | `./data/chat2db.db` | **Deprecated**：仅 `META_DB_DRIVER=sqlite` 时用作 `META_DB_DSN` 的兼容回退，新部署请直接用 `META_DB_DSN` |
| `META_DB_MAX_OPEN_CONNS` | `20` | 元数据库最大连接数（sqlite 下忽略） |
| `META_DB_MAX_IDLE_CONNS` | `5` | 元数据库空闲连接数 |
| `META_DB_CONN_MAX_LIFETIME` | `1h` | 元数据库连接最大生命周期，Go duration 格式 |
| `META_DB_AUTO_MIGRATE` | `true` | sqlite 下使用 GORM AutoMigrate；pg/mysql 下启动时执行 golang-migrate up。生产若迁移已 out-of-band 跑过，可设为 `false`，启动时只做 schema 一致性校验 |
| `JWT_SECRET` | `please-change-me-in-production` | JWT 签名密钥，**生产务必替换**；release 模式下必须覆盖 |
| `JWT_EXPIRE_HOURS` | `72` | JWT 有效期 |
| `CREDENTIAL_KEY` | 占位 32 字节 | AES-256-GCM 主密钥，**必须 ≥ 32 字节**，用于加密 DB 密码与 LLM API Key；release 模式下必须覆盖 |
| `QUERY_MAX_ROWS` | `1000` | 单次 SQL 执行最多返回行数，超出会标记 `truncated` |
| `QUERY_TIMEOUT_SECONDS` | `30` | 单次 SQL 执行超时 |
| `AUDIT_RETENTION` | `2160h`（90 天） | 审计日志保留时长，Go duration 格式。`<=0` 表示永不清理 |
| `LOG_LEVEL` | `info` | slog 全局输出级别：`debug` / `info` / `warn` / `error`。所有日志为 JSON 结构化输出到 stdout，包含 `request_id` 字段便于跨条目串联 |

> ⚠️ `CREDENTIAL_KEY` 一旦更换，原先加密过的 DB 密码 / LLM API Key 将**无法解密**，所有连接会需要重新保存一次密码。

## API 速查

所有受保护接口都需要 `Authorization: Bearer <token>`。

| Method | Path | 说明 |
|--------|------|------|
| `POST` | `/api/auth/register` | 注册 |
| `POST` | `/api/auth/login` | 登录，返回 JWT |
| `GET`  | `/api/health` | 健康检查 |
| `GET`  | `/api/me` | 当前用户 + LLM 配置状态 |
| `PUT`  | `/api/me/llm` | 更新自己的 LLM 配置 |
| `GET`  | `/api/groups` | 我所在的连接组列表 |
| `POST` | `/api/groups` | 新建组（创建者自动 Owner） |
| `PUT`  | `/api/groups/:groupID` | 改组名 / 描述 / ShareLLM（Owner） |
| `GET`  | `/api/groups/:groupID/members` | 组成员列表 |
| `POST` | `/api/groups/:groupID/members` | 邀请 / 更新成员 |
| `DELETE` | `/api/groups/:groupID/members/:userID` | 移除成员（Owner） |
| `GET`  | `/api/groups/:groupID/connections` | 组内连接 |
| `POST` | `/api/groups/:groupID/connections` | 新建连接（Owner） |
| `PUT`  | `/api/connections/:connID` | 更新连接（Owner） |
| `DELETE` | `/api/connections/:connID` | 删除连接（Owner） |
| `POST` | `/api/connections/test` | 测试连接（支持已保存或草稿） |
| `GET`  | `/api/connections/:connID/databases` | 实例上的**数据库列表**（PG 列 `pg_database`，MySQL 列 `SHOW DATABASES`） |
| `GET`  | `/api/connections/:connID/schemas?database=` | Schema 列表，可用 `?database=` 临时切换目标库 |
| `GET`  | `/api/connections/:connID/tables?schema=&database=` | 指定 schema 下的表/视图 |
| `GET`  | `/api/connections/:connID/columns?schema=&table=&database=` | 列信息 |
| `GET`  | `/api/connections/:connID/ddl?schema=&table=&database=` | 生成可读 DDL |
| `POST` | `/api/connections/:connID/execute?database=` | 执行 SQL（经 SQL 解析器拦截，可切换目标库） |
| `GET`  | `/api/groups/:groupID/saved-queries` | 组内收藏 SQL |
| `GET`  | `/api/me/saved-queries` | 我能看到的所有收藏 SQL |
| `POST` | `/api/saved-queries` | 新建收藏 |
| `DELETE` | `/api/saved-queries/:id` | 删除（作者或 Owner） |
| `POST` | `/api/ai/chat` | AI 写 SQL，自动注入 @ 引用表的 DDL 上下文 |
| `GET`  | `/api/audit/logs` | 审计日志查询（仅在任一组是 admin/owner 的用户可读，按组隔离） |
| `GET`  | `/api/audit/actions` | 审计事件枚举（供 UI 构造下拉过滤） |

## 默认快捷键

| 位置 | 按键 | 动作 |
|------|------|------|
| SQL 编辑器 | `⌘/Ctrl + Enter` | 执行（选中则执行选中） |
| SQL 编辑器 | `⌥/Alt + I` | 唤出 AI 对话框 |
| SQL 编辑器 | `⌘/Ctrl + S` | 收藏到组 |
| AI 对话框 | `@` | 唤起表补全下拉 |
| 侧边栏右边缘 | 拖拽 / 双击 | 调整宽度 / 恢复默认 |
| 编辑器/结果区之间 | 拖拽 / 双击 | 调整高度比例 / 恢复默认 |

## 数据与安全性说明

- **应用自身的数据**全部落在 SQLite（`META_DB_PATH`）：账号、组、成员、连接、收藏 SQL、加密后的 DB/SSH 凭据与证书、加密后的 LLM 配置。**目标数据库的数据不会被读写到这个文件**。
- **目标数据库账号**：由组 Owner 创建连接时填写；被加密后存储，运行时才解密并传给驱动（pgx / database/sql）。**强烈建议在生产库上给本服务建独立只读账号，即使一时粗心给了 Viewer 一条 `DROP` 语句，数据库层也会拒绝。** sqlguard 是第一道防线，数据库权限是最终兜底。
- **SSH 隧道**：只在后端进程内存里维护隧道条目，进程重启或连接凭据更新都会立刻失效旧隧道；私钥 / Passphrase 永远只驻留在后端内存。
- **多租户隔离**：所有查询都必须先通过 `RequireRole(actorID, groupID, …)`，业务路径不会绕过组的 membership 检查。
- **日志**：默认 Gin access log，不会打印 SQL 参数或密码；但你自己在前端看到报错时上报截图请自行遮挡。

## 路线图 / TODO

- [x] MySQL 驱动
- [x] SSH 隧道 + 双向 SSL
- [x] 表数据单元格内联编辑（带权限校验）
- [x] 数据库层级切换（会话级别）
- [ ] MariaDB / ClickHouse / SQL Server 驱动
- [ ] 组内收藏 SQL 的版本化 / 协作编辑
- [ ] 审计日志（谁、什么时间、哪条 SQL）
- [ ] Docker 官方镜像 + Compose
- [ ] 切换到 PostgreSQL 作为元数据库（可选）
- [ ] 2FA / SSO（OIDC）

## License

MIT
