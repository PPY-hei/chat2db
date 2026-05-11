# Chat2DB Web

> 一个轻量、Web 化的 PostgreSQL 可视化管理平台，支持多人协作、基于角色的 SQL 权限拦截、团队共享 AI 辅助写 SQL。后端 Go + Gin + GORM，前端 React + AntD + Monaco。

UI 参考了 VSCode 的树形结构与 Navicat 的表数据浏览，定位是**可以丢在内网给团队使用的"数据库管理协作台"**：

- 每个连接分配到一个"连接组"，组 = 共享单元。
- 账号之间不共用数据库账号，权限完全由**应用层 RBAC + SQL 解析器**控制，杜绝"Viewer 执行 DROP"这种越权。
- 支持 AI 对话写 SQL，组 Owner 可共享自己的 LLM 配置给组内成员，无需每人一份 API Key。

## 目录

- [Chat2DB Web](#chat2db-web)
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
| 数据源 | PostgreSQL（`pgx` 池化连接，按连接缓存并在编辑/删除时自动失效） |
| 浏览 | VSCode 风格左侧树：连接组 → 连接 → Schema → 表/视图；懒加载 + 虚拟滚动；支持按组刷新 Schema |
| 表数据 | 分页（20/50/100/200/500）、列头三态排序、多条件筛选（含 `contains`、`IN`、`IS NULL`、bool 列专用下拉）、COUNT(*) 真实总数、`查看 DDL`、`在 SQL 窗口打开` |
| SQL 窗口 | Monaco 编辑器、多 Tab、拖拽调整 SQL/结果区比例、选中执行、结果复制 TSV / 导出 CSV |
| SQL 权限拦截 | 后端 SQL 解析器按语句分类（read/write/ddl/admin/tx）匹配角色白名单，支持多语句、注释、字符串、`$tag$` dollar-quoted、CTE 写操作保守策略 |
| AI 写 SQL | OpenAI 兼容接口，任何 endpoint 可接；输入框 `@` 引用当前连接的表，自动把引用表 DDL 一起发送给模型；引用以 Tag 展示并可删除；Owner 可把 LLM 配置共享给组内成员 |
| 团队共享 SQL | 任意组员可收藏 SQL 到组内（标题必填、描述可选）；组内所有成员可见、可"插入到光标"；个人收藏视图聚合所有组内收藏，一键跳转执行 |
| UX | 侧边栏宽度可拖拽、SQL 编辑器/结果区比例可拖拽、尺寸持久化到 localStorage；树结构虚拟化避免大表性能问题 |

## 技术栈与架构

### 后端

- Go 1.24
- [Gin](https://github.com/gin-gonic/gin) HTTP 框架
- [GORM](https://gorm.io) + SQLite 作为**应用元数据库**（账号 / 组 / 成员 / 连接 / 收藏 SQL）
- [pgx v5](https://github.com/jackc/pgx) 连接目标 PostgreSQL 实例（池化，每连接独立池）
- [golang-jwt](https://github.com/golang-jwt/jwt) JWT 签发/校验
- `golang.org/x/crypto/bcrypt` 账号密码
- `crypto/aes`（AES-256-GCM）加密数据库密码 / LLM API Key
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
│  │ sqlguard         │ dbexec (pgx pool per conn)│               │
│  │ (permission)     │ meta / exec / DDL gen     │               │
│  ├──────────────────┼────────────────────────────┤              │
│  │ llm.Chat         │  crypto (AES-GCM)         │               │
│  │ (OpenAI compat)  │                           │               │
│  └──────────────────┴────────────────────────────┘              │
│  GORM + SQLite (app metadata) ───► chat2db.db                   │
└─────────────────────────────────────────────────────────────────┘
        │                       │                     │
        ▼                       ▼                     ▼
 PostgreSQL 实例 A       PostgreSQL 实例 B      OpenAI 兼容 API
 （组成员共享）          （组成员共享）         （可选、用户级/组共享）
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
│       ├── dbexec/                 # pgx 池化 + 元数据 + DDL 生成
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

| 动作 | Viewer | Editor | Owner |
|------|:------:|:------:|:------:|
| 查看连接 / Schema / 表 | ✓ | ✓ | ✓ |
| 执行 SELECT / SHOW / EXPLAIN | ✓ | ✓ | ✓ |
| 执行 INSERT / UPDATE / DELETE / MERGE | ✗ | ✓ | ✓ |
| 执行 BEGIN / COMMIT / ROLLBACK | ✗ | ✓ | ✓ |
| 执行 CREATE / ALTER / DROP / TRUNCATE | ✗ | ✗ | ✓ |
| 执行 GRANT / REVOKE / VACUUM / ANALYZE | ✗ | ✗ | ✓ |
| 邀请新成员（viewer / editor） | ✗ | ✓ | ✓ |
| 邀请 / 提升为 Owner | ✗ | ✗ | ✓ |
| 修改既有成员角色 / 移除成员 | ✗ | ✗ | ✓ |
| 新建 / 编辑 / 删除连接 | ✗ | ✗ | ✓ |
| 切换 ShareLLM / 改组名描述 | ✗ | ✗ | ✓ |
| 收藏 SQL | ✓ | ✓ | ✓ |

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

- 数据库密码 / LLM API Key 都用 AES-256-GCM（32 字节 key）加密落 SQLite。
- pgx 查询统一走参数化（元数据查询如 `ListTables` / `ListColumns` 用 `$1/$2`），用户层 SQL 走 SQL 解析器白名单。
- JWT 通过 HS256 签发；未带/过期 token 返回 401，前端全局拦截器会自动跳回登录页。
- LLM API Key 永远只驻留在后端内存，通过服务端代理转发，不会在 `/api/me` 等接口返回给前端。

## 本地开发

### 先决条件

- Go ≥ 1.22（推荐 1.24）
- Node ≥ 18
- 一套可访问的 PostgreSQL 实例（10+ 都可）
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
FROM golang:1.24-alpine AS backend-builder
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
| `SERVER_MODE` | `debug` | `debug` / `release`（对应 gin 模式） |
| `META_DB_PATH` | `./data/chat2db.db` | 应用元数据 SQLite 路径 |
| `JWT_SECRET` | `please-change-me-in-production` | JWT 签名密钥，**生产务必替换** |
| `JWT_EXPIRE_HOURS` | `72` | JWT 有效期 |
| `CREDENTIAL_KEY` | 占位 32 字节 | AES-256-GCM 主密钥，**必须 ≥ 32 字节**，用于加密 DB 密码与 LLM API Key |
| `QUERY_MAX_ROWS` | `1000` | 单次 SQL 执行最多返回行数，超出会标记 `truncated` |
| `QUERY_TIMEOUT_SECONDS` | `30` | 单次 SQL 执行超时 |

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
| `GET`  | `/api/connections/:connID/schemas` | Schema 列表 |
| `GET`  | `/api/connections/:connID/tables?schema=` | 指定 schema 下的表/视图 |
| `GET`  | `/api/connections/:connID/columns?schema=&table=` | 列信息 |
| `GET`  | `/api/connections/:connID/ddl?schema=&table=` | 生成可读 DDL |
| `POST` | `/api/connections/:connID/execute` | 执行 SQL（经 SQL 解析器拦截） |
| `GET`  | `/api/groups/:groupID/saved-queries` | 组内收藏 SQL |
| `GET`  | `/api/me/saved-queries` | 我能看到的所有收藏 SQL |
| `POST` | `/api/saved-queries` | 新建收藏 |
| `DELETE` | `/api/saved-queries/:id` | 删除（作者或 Owner） |
| `POST` | `/api/ai/chat` | AI 写 SQL，自动注入 @ 引用表的 DDL 上下文 |

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

- **应用自身的数据**全部落在 SQLite（`META_DB_PATH`）：账号、组、成员、连接、收藏 SQL、加密后的 LLM 配置。**目标数据库的数据不会被读写到这个文件**。
- **目标数据库账号**：由组 Owner 创建连接时填写；被加密后存储，运行时才解密并传给 pgx。**强烈建议在生产库上给本服务建独立只读账号，即使一时粗心给了 Viewer 一条 `DROP` 语句，数据库层也会拒绝。** sqlguard 是第一道防线，数据库权限是最终兜底。
- **多租户隔离**：所有查询都必须先通过 `RequireRole(actorID, groupID, …)`，业务路径不会绕过组的 membership 检查。
- **日志**：默认 Gin access log，不会打印 SQL 参数或密码；但你自己在前端看到报错时上报截图请自行遮挡。

## 路线图 / TODO

- [ ] MySQL / MariaDB / ClickHouse / SQL Server 驱动
- [ ] 组内收藏 SQL 的版本化 / 协作编辑
- [ ] 审计日志（谁、什么时间、哪条 SQL）
- [ ] Docker 官方镜像 + Compose
- [ ] 切换到 PostgreSQL 作为元数据库（可选）
- [ ] 2FA / SSO（OIDC）
- [ ] 表数据单元格内联编辑（带权限校验）

## License

MIT
