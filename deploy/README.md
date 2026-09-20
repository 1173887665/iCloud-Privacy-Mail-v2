# 服务器部署模板

这组文件让 iCloud Privacy Mail 与服务器上已有的其他网站共存。推荐给它分配独立子域名，例如 `mail.example.com`，由 Nginx 负责 HTTPS 和域名转发，程序本身只监听服务器本机的 `127.0.0.1:8788`。

## 方案 A：Docker Compose（推荐）

以下命令在仓库根目录执行：

```bash
cp deploy/config.container.example.json config.json
mkdir -p data
# 绑定宿主机目录时，让容器用户可以写入 SQLite 和备份文件
sudo chown -R 10001:10001 data
# 编辑 config.json：把 public_base_url 改成真实的 https 地址
docker compose -f deploy/docker-compose.yml config
IPM_COMMIT="$(git rev-parse HEAD)" docker compose -f deploy/docker-compose.yml up -d --build
docker compose -f deploy/docker-compose.yml ps
curl http://127.0.0.1:8788/api/health
```

Compose 只把宿主机的 `127.0.0.1:8788` 映射给容器，因此不会抢占已有网站的 80/443 端口。数据保存在仓库的 `data/` 中，升级镜像时不会丢失数据库。

如 8788 已被占用，可在命令前设置其他本机端口：

```bash
IPM_PORT=18788 IPM_COMMIT="$(git rev-parse HEAD)" docker compose -f deploy/docker-compose.yml up -d --build
```

此时 Nginx 的 `proxy_pass` 也要改为 `http://127.0.0.1:18788`。

### 从 GitHub 更新 Docker 部署

程序会在“版本与更新”和公告中心读取 GitHub 默认分支的最新提交；提交 SHA 变化时显示更新提示。提示只负责通知和确认，服务器不会让正在运行的进程覆盖自身。确认后在服务器执行：

```bash
cd /opt/iCloud-Privacy-Mail-v2
sudo deploy/update-server.sh --mode docker
```

脚本会拒绝带有本地未提交改动的工作树，备份 `config.json`、SQLite 数据库和密钥，执行 `git fetch`/快进更新，重建容器并访问 `/api/health`。健康检查失败时会回到更新前的提交并重新启动容器。建议先确认 `git remote -v` 指向自己的仓库，并保留服务器 SSH 或控制台回滚入口。

## 方案 B：Linux 原生 systemd

先在 Linux 上构建二进制（或使用 CI 交叉编译），然后将 `bin/ipm-server`、`config.json` 和 `data/` 放到 `/opt/iCloud-Privacy-Mail-v2/`。配置示例见 `config.native.example.json`。

```bash
sudo useradd --system --home /opt/iCloud-Privacy-Mail-v2 --shell /usr/sbin/nologin ipm
sudo mkdir -p /opt/iCloud-Privacy-Mail-v2/data
sudo chown -R ipm:ipm /opt/iCloud-Privacy-Mail-v2
sudo cp deploy/systemd/icloud-privacy-mail.service.example /etc/systemd/system/icloud-privacy-mail.service
sudo systemctl daemon-reload
sudo systemctl enable --now icloud-privacy-mail
curl http://127.0.0.1:8788/api/health
```

### 从 GitHub 更新 systemd 部署

```bash
cd /opt/iCloud-Privacy-Mail-v2
sudo deploy/update-server.sh --mode systemd
```

该模式需要服务器安装 Go。脚本将当前二进制保存为 `bin/ipm-server.previous`，用最新源码构建并注入提交 SHA，重启 `icloud-privacy-mail` 服务，再做健康检查；失败会恢复上一份二进制。配置和 `data/` 不会从 GitHub 覆盖。

如果不希望使用默认路径，可通过环境变量覆盖，例如：

```bash
sudo IPM_APP_DIR=/srv/ipm IPM_SERVICE=ipm IPM_HEALTH_URL=http://127.0.0.1:18788/api/health deploy/update-server.sh --mode systemd
```

每次提交都会被检测到，但生产服务器建议在确认邮件同步和 Apple 登录状态正常后再执行更新。若要无人值守自动更新，应另行配置受限的 systemd timer，并先在预发布实例验证；本项目默认不自动重启生产服务。

## Nginx 与已有网站共存

1. 将 `deploy/nginx/ipm.conf.example` 复制到 Nginx 的 `sites-available`，把 `mail.example.com` 换成真实子域名。
2. 在 DNS 中添加 `mail` 子域名，指向服务器公网 IP。
3. 启用配置并检查：

   ```bash
   sudo nginx -t
   sudo systemctl reload nginx
   ```

4. 用 Certbot 为这个子域名签发证书：

   ```bash
   sudo certbot --nginx -d mail.example.com
   ```

`proxy_buffering off` 和较长的 `proxy_read_timeout` 是必须的，因为后台使用 `/api/realtime` Server-Sent Events（SSE）。

## 安全与数据迁移

- HTTPS 正式上线时保持 `secure_cookie: true`；不要在公网直接暴露 8788。
- 若迁移当前电脑的数据，必须同时复制 `data/app.db` 和 `data/app.db.key`，并在停机状态复制，避免 SQLite 文件不一致。
- 首次打开会创建本地管理员，项目没有默认用户名或密码。
- 不要把 `config.json`、`data/app.db`、`data/app.db.key` 提交到 Git；仓库的 `.gitignore` 已忽略它们。
- 迁移后先访问 `/api/health`，再打开域名测试登录、实时状态和邮件同步。
