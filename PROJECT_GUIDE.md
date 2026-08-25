# iCloud Privacy Mail 项目指南

> 文档版本：2026-08-15
>
> 项目目录：`iCloud-Privacy-Mail-v2`
>
> 项目来源：[xiuxiu56/iCloud-Privacy-Mail-v2](https://github.com/xiuxiu56/iCloud-Privacy-Mail-v2)
>
> 运行定位：单管理员、本地优先的 Apple 隐私邮箱管理工具

本项目在来源仓库中统一维护 Apple/iCloud 协议、Go 后端、Vue 3 管理页面和项目文档。当前版本已完成 SQLite 数据基座、SSE 实时更新、新版紧凑 UI、邮箱租约和后台任务队列。

文档截图由当前仓库源码重新构建，并从隔离演示实例采集，只含 `demo.*` 示例数据，不包含真实 Apple 账号、邮箱、Cookie、密码、Token 或邮件。

## 1. 能力概览

- 首次运行创建唯一的本地管理员，后续访问管理页面必须登录。
- Apple Account 新接口与 iCloud Web 旧接口登录、2FA、登录态检测和后台保活。
- iCloud IMAP App 专用密码验证、IMAP IDLE 监听、账号级批量同步和验证码提取。
- 隐私邮箱创建、远端同步、手动导入、筛选、多选、快捷编辑、详情和删除。
- 全量 Apple 邮件清理、按本地已同步邮件精确清理、邮箱彻底删除。
- 单次创建与多账号自动创建，轮次和账号间隔均为随机范围。
- 公共取号租约 API、邮箱独立取码 API、公共邮箱取码与邮件查看页面。
- 运行数据、邮件、邮箱地址和取码 API 导出。
- SQLite WAL、逐版本迁移、AES-GCM 字段加密、在线备份与自动维护。
- SSE 变更序号、持久化日志、心跳和断线回放。
- GitHub 版本检查、构建信息和公告中心。

项目不包含多用户注册、用户管理后台和 `/manage` 页面。访问 `/manage` 或 `/manage/` 固定返回 404。

## 2. 快速开始

### 2.1 环境要求

| 工具 | 版本 | 用途 |
| --- | --- | --- |
| Go | `go.mod` 最低 1.25；推荐 1.26.6 或更新补丁版本 | 后端编译与运行 |
| Node.js | 满足 Vite 8 要求 | 构建 Vue 前端 |
| npm | 与 Node.js 配套 | 安装锁定的前端依赖 |

推荐 Go 1.26.6 的原因是本次 `govulncheck` 在 Go 1.26.5 标准库中发现 5 条代码可达漏洞，均标记为在 1.26.6 修复。

### 2.2 首次构建

```bash
cp config.example.json config.json
./scripts/build.sh
go run main.go
```

无参数执行 `go run main.go` 会打开中文启动菜单。选择启动服务后访问：

```text
http://127.0.0.1:8788/
```

项目没有预设密码。首次打开会要求创建管理员账号和至少 8 位密码。

### 2.3 常用启动方式

```bash
# 使用默认 config.json 并显示菜单
go run main.go

# 跳过菜单，直接按配置启动
go run main.go -config config.json

# 显式打开菜单
go run main.go -menu

# 临时覆盖监听地址、端口和数据库
go run main.go -config config.json -host 127.0.0.1 -port 8788 -data data/app.db

# 运行构建产物
./bin/ipm-server -config config.json
```

### 2.4 开发模式

```bash
./scripts/dev.sh
```

- Vue 开发地址：`http://127.0.0.1:5174/`
- Go API 地址：`http://127.0.0.1:8788/`
- Vite 把 `/api` 请求代理到 Go 服务。

`go run main.go` 使用 `internal/webui/dist` 中的 Go 嵌入式资源，不直接读取 `frontend/src`。只重新生成内嵌前端时执行：

```bash
npm --prefix frontend run build
./scripts/sync-web.sh
go run main.go
```

## 3. 新版 UI 设计

新版页面参考紧凑型注册任务控制台的视觉密度，统一了字体、卡片、选择器、弹窗、输入框和按钮尺寸：

- 左侧为筛选和输入控件，右侧为主要操作按钮。
- 表头字号略大于表格内容，内容按列居中，详情文字使用普通字重。
- 选择器下拉项字号不大于当前选中值，选项卡宽度与选择器一致。
- 未启动状态、设置图标和启动按钮保持等高。
- 表格根据浏览器可视高度计算每页数量，分页信息放在表底。
- 通用弹窗统一标题、正文、按钮和单行备注控件。
- 异步操作只锁定当前资源或冲突动作，不全局禁用其他按钮。

### 3.1 登录页 `/login`

![新版登录页](./docs/screenshots/ui/00-login.jpg)

登录页承担首次设置和后续登录两种状态。密码输入框提供显示/隐藏按钮。登录成功后使用 HttpOnly、SameSite Strict Cookie 保存会话，默认有效期为 168 小时；HTTPS 部署时应开启 `secure_cookie`。

### 3.2 控制台 `/`

![新版控制台](./docs/screenshots/ui/01-dashboard.jpg)

控制台展示：

- Apple 账号总数和正常账号数。
- 隐私邮箱总数和可用数量。
- 本地邮件缓存数量。
- IMAP 监听、Apple 保活、自动创建和公共接口状态。
- 最近运行记录。

运行记录沿用创建页“调度日志”的表格设计，图标与各列内容居中；事件信息靠左、时间靠右。表格高度按当前浏览器可容纳的记录数计算，避免表底出现无意义空块。

### 3.3 Apple 账号 `/apple-accounts`

![Apple 账号列表](./docs/screenshots/ui/02-apple-accounts.jpg)

![Apple 登录态详情](./docs/screenshots/ui/02b-apple-account-detail.jpg)

页面集中管理三类能力：

| 登录态 | 用途 |
| --- | --- |
| Apple Account | 优先创建隐私邮箱、管理新接口登录态 |
| iCloud Web | 同步远端邮箱、兼容创建、邮件和邮箱远端操作 |
| iCloud IMAP | 接收邮件、实时监听和提取验证码 |

推荐顺序：

1. 点击“添加 Apple 账号”。
2. 选择 Apple Account 或 iCloud Web，输入 Apple ID 和密码。
3. Apple 要求 2FA 时提交可信设备或短信验证码。
4. 点击账号整行打开登录态详情和检测结果。
5. 保存 IMAP 邮箱和 App 专用密码。
6. 检测登录态，随后创建或同步隐私邮箱。

账号输入框和 IMAP 输入框使用与其他页面一致的前置图标结构，图标不会覆盖输入内容。删除账号会清理本地账号、登录态、关联邮箱和本地邮件，不会直接删除 Apple 云端隐私邮箱。

### 3.4 邮箱池 `/mailboxes`

![新版邮箱池](./docs/screenshots/ui/03-mailboxes.jpg)

![邮箱详情](./docs/screenshots/ui/03b-mailbox-detail.jpg)

表格包含 ID、复选框、邮箱、标签/备注、状态、API/iCloud、收件、最近同步和操作：

- 表头间距平均分配，邮箱和其余内容均在对应列居中。
- 点击邮箱地址自动复制。
- 标签和备注分行展示；备注编辑为单行输入框，可以提交空值清除旧备注。
- 点击状态可直接选择并保存。
- 点击“详情”查看普通字重的字段、邮件列表和完整邮件内容。
- 点击“取码”打开同一个状态弹窗；收到验证码后在原弹窗内切换结果，验证码可点击复制。
- 行级同步只锁定当前邮箱，其他邮箱按钮保持可操作。

邮箱池不提供固定“每页数量”设置。页面根据窗口高度自动计算可显示行数，并在表底显示：

```text
第 1 / 17 页，总 114 个邮箱
```

#### Apple 邮件清理

“全部彻底清理 Apple 邮件”会按 Apple 账号排队，扫描所有远端邮件文件夹并清理云端与本地邮件。进度格式为：

```text
全部邮件清理：Apple 账号 2｜邮箱 119｜执行中 1｜排队中 1｜已完成 0/119（成功 0，失败 0）｜当前文件夹 收件箱（新闻·重点）
```

其中：

- `Apple 账号` 是参与清理的账号任务数。
- `邮箱` 是这些账号下需要统计的隐私邮箱总数。
- `执行中/排队中` 按账号任务显示。
- `已完成 A/M` 的分母使用邮箱总数，不使用账号数。
- 邮件为空时仍计入已扫描邮箱；发现、移入废纸篓、彻底删除和本地清理均为 0。
- Apple 内部文件夹名统一翻译成中文，不直接展示 `$category$_News_HI` 等协议值。

详情中的“清理 Apple 远端邮件”根据本地已同步邮件保存的 `RemoteID/UID` 精确定位，将命中的邮件移入 Apple 废纸篓，并可清空该 Apple 账号的整个废纸篓；未同步到本地的历史邮件不参与定位。彻底删除邮箱复用同一套已同步邮件清理方法，然后删除 Apple 隐私邮箱；远端确认不存在后才删除本地邮箱和邮件。

删除选中邮箱使用按 Apple 账号分组的队列，每个邮箱保留二次确认。同一 Apple 账号内串行执行，不同账号最多 4 路并发；队列中当前项显示“删除中”，等待项显示“排队中”，其他不冲突的同步、取码和详情操作不被统一禁用。

“批量删除指定邮箱”支持在普通多行输入框中按一行一个邮箱提交。地址会自动去重并按输入顺序加入对应 Apple 账号的彻底删除队列；未在本地邮箱池找到的地址会单独提示，不影响其他已找到邮箱继续删除。

### 3.5 创建隐私邮箱 `/tasks`

![创建隐私邮箱](./docs/screenshots/ui/04-tasks.jpg)

页面支持两种执行方式：

- 创建一个：默认选择首个可用 Apple 账号，立即执行一次。
- 自动创建：选择一个或多个账号，按随机轮次间隔持续执行；同一轮账号之间也使用随机间隔。

执行方式选择器与内容卡片等宽。账号、创建通道、标签前缀和备注采用一行紧凑布局；账号和创建通道保留足够宽度，标签前缀和备注位于创建通道右侧。

创建通道可选：

- 自动接口：新接口优先，失败时使用旧接口。
- Apple Account 新接口。
- iCloud Web 旧接口。

标签输入的是前缀。留空时默认使用 `x`，服务生成 `x_1`、`x_2` 等连续标签。

自动创建遇到任一创建失败会立即停止，页面按钮和状态恢复为未启动，失败原因写入调度日志。

#### 创建与调度设置

![创建与调度设置](./docs/screenshots/ui/04b-task-settings.jpg)

设置弹窗包括：

- 默认标签前缀与默认备注，同一行显示。
- 默认参与 Apple 账号。
- 默认创建通道与 2FA 方式。
- 下一轮间隔：随机最少分钟到最多分钟。
- 账号间隔：随机最少秒到最多秒。

四个范围值都是普通文本输入框，并在保存时解析为正整数；不使用浏览器数字输入框的上下调节按钮。新版本只使用随机范围字段，不读取旧的单值间隔字段。

任务概览显示执行方式、参与账号、创建通道、成功/失败和执行时间。调度日志包含事件、邮箱、标签、详情和时间，表头均分，内容居中；失败详情直接显示如“单次创建隐私邮箱失败”，不再附加重复状态块。

### 3.6 本地导出 `/exports`

![本地导出](./docs/screenshots/ui/05-exports.jpg)

| 导出项 | 格式 | 内容 |
| --- | --- | --- |
| 运行数据 | JSON | Apple 账号、邮箱、登录态和设置，不含邮件正文 |
| 运行数据与邮件 | JSON | 完整运行数据和本地邮件 |
| 邮箱地址 | TXT/CSV/TSV/JSONL | 隐私邮箱地址 |
| 取码 API | TXT/CSV/TSV/JSONL | 邮箱和独立取码地址 |

完整运行数据可能包含 Apple Cookie、登录态、IMAP App 专用密码、公共 API Key 和邮箱 API Token，应存放在可信位置。

### 3.7 系统设置 `/settings`

![系统设置](./docs/screenshots/ui/06-settings.jpg)

页面分为：

1. 本地数据：邮件保留天数、备份目录、SQLite 路径和数据库维护。
2. 公共访问与后台能力：同一行左右两张等宽卡片。
3. 公共取号 API Key：生成新 Key 与接口说明左右各占一半。
4. 版本与更新：版本、提交、平台、检查时间和 GitHub 内容。

公共取号 API 说明与“生成新 Key”按钮同行，`/email-code` 的“打开页面”链接靠最右侧。邮箱池分页数量由页面自动计算，因此设置页不再提供每页数量字段。

数据库区域提供状态、完整性检查、立即备份和空间整理。后台每天自动执行邮件保留清理、在线备份与 WAL checkpoint。

### 3.8 公共邮箱取码与邮件查看 `/email-code`

![公共验证码页面](./docs/screenshots/ui/07-verification-code.jpg)

页面使用独立布局，不显示后台导航。管理员开启“公共邮箱取码页面”后，外部用户可以输入完整邮箱地址：点击“获取验证码”会在弹窗中展示并支持复制；点击“获取邮件”会同步最近邮件，在下方列表中点击即可查看完整 HTML 或纯文本正文。验证码默认匹配 OpenAI 关键词并使用 5 分钟新鲜窗口。

当前访问模型适合受控环境；公网开放前应增加访问凭证、统一失败响应和速率限制，详见安全审计。

## 4. 数据与实时更新架构

### 4.1 SQLite 唯一数据源

`data/app.db` 是唯一业务数据源，服务不读取或生成 `state.json`。主要表保存：

- 管理员密码摘要与会话摘要。
- Apple 账号、iCloud Session、Apple Account 登录态和 IMAP 配置。
- 隐私邮箱、邮件、租约与同步游标。
- 系统设置、创建设置、运行事件与调度状态。
- SSE `change_log` 与 schema 迁移记录。

SQLite 配置：

- WAL 模式。
- 单连接写入，避免并发写锁争用。
- 外键/关系保护和 5 秒 busy timeout。
- schema 版本 4，启动时按 `schema_migrations` 自动升级。
- 邮箱分页、筛选和邮件查询直接执行 SQL。
- 同账号邮件和游标通过单事务批量提交。

### 4.2 敏感字段

Apple Cookie、登录态、IMAP App 专用密码、邮箱 API Token 和公共 API Key 使用 `data/app.db.key` 进行 AES-GCM 加密。数据库与密钥权限为 `0600`，目录权限为 `0700`。

数据库备份必须同时保留 `.db` 与 `.db.key`。丢失密钥后，加密字段不可恢复。

### 4.3 SSE 实时更新

登录后的前端连接：

```text
GET /api/realtime
```

服务把数据库变化追加到 `change_log`，SSE 事件携带递增 ID。浏览器断线重连时提交 `Last-Event-ID`，服务回放尚在保留范围内的变化；无变化时发送心跳。各页面按事件主题刷新受影响的数据，避免固定高频轮询。

SSE 连接异常只影响即时刷新，不影响普通管理 API。页面重新打开或手动操作仍会读取 SQLite 的最新数据。

## 5. 常用流程

### 5.1 建立邮箱池

```text
创建本地管理员
  → 添加 Apple 账号并完成 2FA
  → 保存 IMAP App 专用密码
  → 检测登录态
  → 同步 Apple 已有邮箱或创建新邮箱
  → 在邮箱池取码、清理和管理状态
```

### 5.2 自动创建

```text
选择“自动创建”
  → 选择参与账号和创建通道
  → 设置标签前缀与备注
  → 在设置弹窗保存随机轮次/账号间隔
  → 启动
  → 通过任务概览、调度日志和 SSE 查看结果
```

### 5.3 公共取号租约

生成并保存公共 API Key，开启公共取号 API。推荐用请求头传递 Key：

```bash
curl -X POST 'https://HOST/api/v1/mailboxes/claim' \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: TOKEN' \
  -d '{"project":"PROJECT","purpose":"PURPOSE","request_id":"REQUEST_ID","note":"备注"}'
```

领取后邮箱进入 `reserved`，响应的 `data.lease.id` 是后续状态变更凭据：

```bash
curl -X POST 'https://HOST/api/v1/mailbox-leases/LEASE_ID/commit' \
  -H 'Content-Type: application/json' \
  -H 'X-API-Key: TOKEN' \
  -d '{"project":"PROJECT","note":"注册成功"}'
```

- 成功：`commit`，执行 `reserved → used`。
- 失败：`release`，恢复 `available`。
- 等待确认：`renew`，延长租约。
- 更新备注：`note`。
- `project + request_id` 保证 claim 幂等重试。

## 6. HTTP 接口

### 6.1 登录、控制台和实时更新

```text
GET  /api/health
GET  /api/auth/status
POST /api/auth/setup
POST /api/auth/login
POST /api/auth/logout
GET  /api/dashboard
GET  /api/events
POST /api/events/clear
GET  /api/realtime
```

### 6.2 Apple 账号和邮件全量清理

```text
GET    /api/apple-accounts
GET    /api/apple-accounts/{id}
DELETE /api/apple-accounts/{id}
POST   /api/apple-accounts/login/start
POST   /api/apple-accounts/login/2fa
POST   /api/apple-accounts/{id}/check
POST   /api/apple-accounts/{id}/imap
POST   /api/apple-accounts/{id}/mailboxes
POST   /api/apple-accounts/{id}/mailboxes/sync
GET    /api/apple-mail/cleanup/status
POST   /api/apple-mail/cleanup
POST   /api/apple-mail/cleanup/cancel
```

### 6.3 邮箱池

```text
GET    /api/mailboxes?q=&status=&account_id=&page=&page_size=
POST   /api/mailboxes
POST   /api/mailboxes/resolve
POST   /api/mailboxes/remote-clean
GET    /api/mailboxes/{id}
POST   /api/mailboxes/{id}/status
POST   /api/mailboxes/{id}/sync
POST   /api/mailboxes/{id}/remote-clean
DELETE /api/mailboxes/{id}
DELETE /api/mailboxes/{id}?local_only=1
GET    /api/mailboxes/{id}/messages
GET    /api/mailboxes/{id}/code
```

### 6.4 调度、设置、数据库和导出

```text
GET  /api/tasks
GET  /api/settings
PUT  /api/settings
GET  /api/create-settings
PUT  /api/create-settings
GET  /api/scheduler/status
POST /api/scheduler/start
POST /api/scheduler/stop
POST /api/scheduler/logs/clear
GET  /api/database/status
POST /api/database/backup
POST /api/database/check
POST /api/database/optimize
GET  /api/update/status
GET  /api/runtime/export
GET  /api/runtime/export?include_messages=1
GET  /api/runtime/export-mailbox-apis?format=txt
GET  /api/runtime/export-mailbox-emails?format=txt
```

### 6.5 公共接口

```text
GET  /api/v1/health
POST /api/v1/mailboxes/claim
POST /api/v1/mailboxes/lookup
GET  /api/v1/mailboxes/{email}/code
GET  /api/v1/mailbox-leases/{lease_id}?project={project}
POST /api/v1/mailbox-leases/{lease_id}/commit
POST /api/v1/mailbox-leases/{lease_id}/release
POST /api/v1/mailbox-leases/{lease_id}/renew
POST /api/v1/mailbox-leases/{lease_id}/note
POST /api/v1/mailboxes/{email}/commit
POST /api/v1/mailboxes/{email}/release
POST /api/v1/mailboxes/{email}/renew
GET  /api/v1/public-code/status
GET  /api/v1/public-code?email={email}
```

邮箱取码查询参数：

| 参数 | 说明 |
| --- | --- |
| `after` | RFC3339 起始时间 |
| `keyword` | 匹配关键词，默认 `OpenAI` |
| `wait_ms` | 等待新邮件毫秒数，最大 30000 |
| `allow_stale` | 同步失败时允许读取本地缓存 |
| `cache` | 只读取本地缓存 |
| `peek` / `preview` | 预览验证码，不标记为已发放 |

## 7. 配置文件

| 字段 | 默认值 | 说明 |
| --- | --- | --- |
| `host` | `127.0.0.1` | HTTP 监听地址 |
| `port` | `8788` | HTTP 端口 |
| `data_path` | `data/app.db` | SQLite 数据库 |
| `session_ttl_hours` | `168` | 管理员会话小时数 |
| `secure_cookie` | `false` | HTTPS 部署时设为 `true` |
| `api_key` | 空 | 公共接口备用全局 Key |
| `public_base_url` | 空 | 生成外部取码地址时使用的基础 URL |
| `icloud_default_host` | `www.icloud.com.cn` | 默认 iCloud 区域主机 |
| `apple_account_api_key` | 空 | Apple Account 管理接口备用 Key |
| `apple_account_keep_alive_enabled` | `true` | 是否加载 Apple 保活能力 |
| `apple_account_keep_alive_ms` | `180000` | 保活基础间隔 |
| `apple_account_keep_alive_jitter_percent` | `15` | 保活随机浮动比例 |
| `mail_watcher_enabled` | `true` | 是否加载 IMAP 监听能力 |
| `mail_watcher_poll_ms` | `3000` | 监听分组重检间隔 |
| `mail_watcher_fetch_limit` | `8` | 常规同步每账号拉取上限 |
| `mail_watcher_initial_fetch_limit` | `20` | 首次监听拉取上限 |
| `mail_watcher_lookback_hours` | `24` | 首次监听回看小时数 |
| `public_fast_sync_wait_ms` | `600` | 公共取码快速同步等待 |
| `public_sync_min_interval_ms` | `3000` | 同邮箱公共同步最短间隔 |
| `public_mailbox_lease_ttl_minutes` | `30` | 新租约默认分钟数 |
| `public_mailbox_lease_max_ttl_minutes` | `10080` | 租约最长分钟数 |
| `public_mailbox_lease_sweep_seconds` | `30` | 过期租约扫描间隔 |
| `database_backup_dir` | `data/backups` | 在线备份目录 |
| `database_backup_retention_days` | `14` | 备份保留天数 |
| `database_message_retention_days` | `90` | 本地邮件保留天数 |
| `database_change_log_limit` | `5000` | SSE 变更日志保留上限 |
| `update_enabled` | `true` | 是否启用版本和公告检查 |
| `update_repository` | `xiuxiu56/iCloud-Privacy-Mail-v2` | GitHub 检查仓库 |

`config.json` 和 `data/` 已加入 `.gitignore`。配置文件包含 Key 时应限制权限：

```bash
chmod 600 config.json
```

## 8. 数据库维护与恢复

服务启动时读取 `schema_migrations` 并顺序执行未应用迁移，不保留旧 JSON 迁移流程。升级前建议在“系统设置 → 本地数据”执行在线备份。

备份同时生成：

```text
app-时间.db
app-时间.db.key
```

恢复步骤：

1. 停止服务。
2. 把备份 `.db` 复制到配置的 `data_path`。
3. 把对应 `.db.key` 复制到 `data_path + ".key"`。
4. 确认目录权限 `0700`，两个文件权限 `0600`。
5. 启动服务并执行完整性检查。

不要只复制正在运行的 WAL 主文件；使用系统设置的在线备份或 SQLite 备份机制获得一致快照。

## 9. 技术结构

```text
浏览器 Vue 页面
  ├── 管理 API ── HttpOnly Cookie
  ├── 实时事件 ── SSE / Last-Event-ID
  └── 公共 API ── API Key / 邮箱 Token
            │
            ▼
httpapi.Server
  ├── auth.Service
  ├── apple.Service ───── protocol.AuthFacade / ICloudClient
  ├── mailbox.Service ─── protocol.IMAP / ICloudClient
  ├── mailwatcher.Service ── IMAP IDLE / 账号级批量同步
  ├── scheduler.Service ──── 随机范围调度 / 持久化日志
  └── store.Store ────────── data/app.db / change_log
```

| 层级 | 技术 | 作用 |
| --- | --- | --- |
| 后端 | Go、`net/http` | API、会话、后台任务和静态资源 |
| 存储 | `modernc.org/sqlite` | WAL、事务、迁移、查询和变更日志 |
| Apple 协议 | Go 协议客户端 | SRP、2FA、Apple Account、iCloud Web、HME |
| 邮件 | IMAP over TLS、iCloud Web 邮件接口 | 同步、IDLE、取码和远端清理 |
| 前端 | Vue 3、Vue Router | 页面、登录守卫和响应式状态 |
| 样式 | Tailwind CSS 4 | 紧凑布局、亮暗主题和响应式设计 |
| 图标 | Lucide Vue | 统一界面图标 |
| 构建 | Vite 8、Go embed | 前端生产构建和单服务运行 |

## 10. 安全与部署

本地使用建议：

- 保持 `host=127.0.0.1`。
- 公共取号和公共验证码默认关闭，按需开启。
- 定期使用在线备份并成对保存 `.db` 与 `.db.key`。
- 生产环境不要开启 Apple 响应正文调试日志。
- 使用 Go 1.26.6 或更新补丁版本重新构建。

公网部署前完成：

1. HTTPS 反向代理与 `secure_cookie=true`。
2. Host allowlist、固定 `public_base_url` 和可信代理规则。
3. 登录、首次设置、公共取号、公共验证码和取码接口限流。
4. 管理写接口 Origin/CSRF 校验。
5. 公共验证码访问凭证与统一错误响应。
6. Apple/iCloud 服务 URL 域名 allowlist。
7. 敏感导出权限控制和离线保管。
8. CSP、Permissions-Policy 和弹窗 focus trap。

所有 `/api/` 响应已经统一设置 `Cache-Control: no-store`。完整问题分级和扫描结果见 [代码与安全审计](docs/07-代码与安全审计.md)。

## 11. 相关文档

- [代码与安全审计](docs/07-代码与安全审计.md)
- [现状分析](docs/01-现状分析.md)
- [重构架构](docs/02-重构架构.md)
- [功能取舍](docs/03-功能取舍.md)
- [SQLite 升级与恢复](docs/04-迁移指南.md)
- [项目结构](docs/05-项目结构.md)
- [缺口审计](docs/06-缺口审计.md)
- [重构总结](REFACTOR_SUMMARY.md)
