# 自建数据同步服务（sessionsvr）

`dschat` 默认是纯本地应用：所有会话都存在本机，不联网同步。
要在多台设备之间同步会话，需要自己跑一个 `sessionsvr`——它就是 dschat 的同步服务端。

它是一个**单二进制程序**：不需要数据库、不需要 Go 环境、不需要容器编排。
一个可执行文件 + 一个配置文件 + 一个数据目录，就能跑起来。

> 如果你只是在一台设备上使用，**不需要部署它**。

---

## 1. 准备

- 一台常开的机器：Linux 服务器、NAS、家里的 Mac 或 Windows 都可以；
- 客户端能访问到的地址与端口（默认 `5218`）；
- 硬盘空间：会话文本很小，占用主要来自对话里的附件。

不需要 Go 开发环境——下面的步骤都用发布好的二进制。

## 2. 下载

打开 [Releases](https://github.com/nuclearg/dschat/releases) 页面，下载对应平台的 `sessionsvr` 压缩包：

| 平台 | 文件名 |
| --- | --- |
| Linux x86_64 | `sessionsvr-linux-amd64.tar.gz` |
| Linux ARM64（部分 VPS、树莓派） | `sessionsvr-linux-arm64.tar.gz` |
| macOS（Apple Silicon） | `sessionsvr-darwin-arm64.tar.gz` |
| Windows x64 | `sessionsvr-windows-amd64.zip` |

解压后会有两个文件：

```
sessionsvr              # Windows 下是 sessionsvr.exe
config.properties.example
```

## 3. 配置

把示例配置复制成正式配置：

```bash
cp config.properties.example config.properties
```

用编辑器打开 `config.properties`，设置密码：

```properties
PASSWORD=换成你自己的密码
```

> **密码为空意味着没有任何鉴权**：能访问到这个地址的人都可以读写你的全部会话。
> 只建议在完全可信的本机或内网环境这么做，客户端选择空密码时也会弹窗确认风险。
>
> 修改密码会让所有已连接的设备登录失效，需要在客户端重新连接一次。

## 4. 启动

```bash
./sessionsvr -addr :5218 -data /var/lib/dschat/sessionsvr -config ./config.properties
```

| 参数 | 说明 | 默认值 |
| --- | --- | --- |
| `-addr` | 监听地址 | `:5218`（所有网卡） |
| `-data` | 数据目录，存放数据库与附件 | `~/.dschat/sessionsvr` |
| `-config` | 配置文件路径 | 可执行文件同目录下的 `config.properties` |

启动成功后日志会打印监听地址、数据目录和配置文件位置：

```
sessionsvr listening on :5218, data /var/lib/dschat/sessionsvr, config ./config.properties, password set
```

首次启动会自动在数据目录里创建：

```
sessionsvr.db        # SQLite 数据库（运行中还会有 -wal / -shm）
attachments/         # 附件原件，上传过附件后才会出现
```

**关于监听地址**：

- `-addr :5218` 监听所有网卡，适合直接在局域网里用；
- `-addr 127.0.0.1:5218` 只监听本机，适合前面挂反向代理（见第 7 节）；
- 也可以写死某个内网 IP，如 `-addr 192.168.1.10:5218`。

## 5. 验证服务是否正常

用 curl 请求一次元信息接口：

```bash
curl -s -X POST http://127.0.0.1:5218/v1/meta.get \
  -H 'Content-Type: application/json' -d '{}'
```

正常会返回类似：

```json
{"success":true,"data":{"authModes":["password"],"passwordSet":true,"protocol":1,"serverId":"01M7..."}}
```

看到 `"success":true` 且 `passwordSet` 与你配置的一致，说明服务已经就绪。

## 6. 在客户端连接

1. 打开 dschat 的 **设置 → 多端同步**；
2. 选择 **连接到自建服务器**；
3. 地址填 `http://你的地址:5218`（例如 `http://192.168.1.10:5218`），密码填第 3 步设置的值；
4. 点 **连接**。

连接成功后：

- **新建的会话**会自动同步到服务端，其他设备登录后能看到；
- **已有的本地会话不会自动上传**，它们仍然是本地会话；
- 断开连接后，原来已同步的会话会还原成本地会话，可以照常继续使用。

## 7. 常驻运行

下面的示例把服务跑在 `127.0.0.1:5218`，并假定：

- 可执行文件放在 `/opt/dschat/sessionsvr`
- 配置在 `/opt/dschat/config.properties`
- 数据目录在 `/var/lib/dschat/sessionsvr`

### Linux（systemd）

创建 `/etc/systemd/system/dschat-sessionsvr.service`：

```ini
[Unit]
Description=dschat sessionsvr
After=network-online.target

[Service]
Type=simple
User=dschat
Group=dschat
ExecStart=/opt/dschat/sessionsvr -addr 127.0.0.1:5218 -data /var/lib/dschat/sessionsvr -config /opt/dschat/config.properties
Restart=on-failure
RestartSec=3

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now dschat-sessionsvr
sudo journalctl -u dschat-sessionsvr -f
```

### macOS（launchd）

创建 `~/Library/LaunchAgents/com.dschat.sessionsvr.plist`：

```xml
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>com.dschat.sessionsvr</string>
  <key>ProgramArguments</key>
  <array>
    <string>/opt/dschat/sessionsvr</string>
    <string>-addr</string><string>127.0.0.1:5218</string>
    <string>-data</string><string>/Users/你的用户名/.dschat/sessionsvr</string>
    <string>-config</string><string>/opt/dschat/config.properties</string>
  </array>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
</dict>
</plist>
```

```bash
launchctl load ~/Library/LaunchAgents/com.dschat.sessionsvr.plist
```

临时用一下也可以直接 `nohup ./sessionsvr ... &`，但重启机器后不会自动起来。

### Windows

用 [NSSM](https://nssm.cc/) 把它注册成服务，或建一个「登录时运行」的计划任务：

```bat
nssm install dschat-sessionsvr C:\dschat\sessionsvr.exe -addr :5218 -data C:\dschat\data -config C:\dschat\config.properties
nssm start dschat-sessionsvr
```

## 8. 反向代理与 HTTPS（推荐）

`sessionsvr` 本身只提供 HTTP，不带 TLS。如果要跨公网使用，请放在反向代理后面。

**Caddy**（自动申请证书，最省事）：

```
chat.example.com {
    reverse_proxy 127.0.0.1:5218
}
```

**Nginx**：

```nginx
server {
    listen 443 ssl;
    server_name chat.example.com;

    ssl_certificate     /etc/letsencrypt/live/chat.example.com/fullchain.pem;
    ssl_certificate_key /etc/letsencrypt/live/chat.example.com/privkey.pem;

    # 附件通过请求体上传，适当放大限制
    client_max_body_size 64m;

    location / {
        proxy_pass http://127.0.0.1:5218;
        proxy_http_version 1.1;
        proxy_set_header Host $host;
        # 实时同步使用 SSE，需要关闭缓冲
        proxy_buffering off;
        proxy_read_timeout 3600s;
    }
}
```

配好后客户端地址填 `https://chat.example.com`（不要带端口和路径）。

## 9. 备份与恢复

要备份的只有数据目录：

```
sessionsvr.db      # 会同时存在 -wal / -shm
attachments/
```

最稳妥的做法是**停服后**整目录打包：

```bash
sudo systemctl stop dschat-sessionsvr
tar -czf sessionsvr-backup-$(date +%F).tar.gz -C /var/lib/dschat sessionsvr
sudo systemctl start dschat-sessionsvr
```

恢复时把目录解回原位置，保持 `config.properties` 里的密码不变即可。

会话删除是**软删除**：服务端会把会话标记为删除，7 天后才连同附件一起清理，期间可以在备份里找回。

## 10. 升级

1. 备份数据目录（见上一节）；
2. 停止服务；
3. 用新版本的二进制替换旧文件；
4. **保留** `config.properties` 和数据目录不动；
5. 重新启动服务。

协议版本（`/v1/meta.get` 里的 `protocol`）不一致时，客户端会拒绝连接，此时两端都应升级到同一版本的 dschat。

## 11. 安全建议

- 不要留空密码；
- 不要把它直接暴露在公网上，前面套反向代理 + HTTPS；
- 用防火墙限制来源，例如只允许自己的固定 IP 或内网网段访问；
- 数据目录权限收紧到运行服务的用户（如 `chmod 700`）；
- 定期备份，并确认备份可以恢复。

## 12. 排错

**启动报 `bind: address already in use`**

端口被占用。换端口（`-addr :5219`），或先找出占用者：

```bash
lsof -nP -iTCP:5218 -sTCP:LISTEN
```

**客户端提示密码错误**

确认 `config.properties` 里是 `PASSWORD=...` 的**原文**，没有多余引号或空格；改密码后需要在客户端重新连接。

**客户端连不上**

先在服务端本机跑第 5 节的 curl；本机能通、客户端不通，通常是防火墙、监听地址（`-addr` 只监听了 127.0.0.1）或反向代理配置的问题。

**附件上传失败 / 大文件传不上去**

反向代理的请求体大小限制（Nginx 的 `client_max_body_size`）。

**同步状态一直显示「已同步」但没有新内容**

检查客户端「设置 → 多端同步」里是否仍显示已连接；断线时客户端会自动重连，也可以断开后重新连接一次。

## 13. 已知限制

- 单实例部署：底层是 SQLite，不支持多副本同时写入；
- 不带内置 TLS，公网使用必须配反向代理；
- 没有账号体系：一个共享密码 + 每台设备的独立令牌，适合个人或小范围自用；
- 只做数据同步，不代理 DeepSeek API——每台设备仍然用自己的 API Key。
