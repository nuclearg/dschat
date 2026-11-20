# dschat 使用指南

dschat 是一个本地优先的 DeepSeek 桌面客户端。聊天记录、附件和 API Key 默认只保存在本机；需要多端同步时，再连接你自己搭建的 sessionsvr。

当前支持：

- macOS 13 Ventura 及以上（Apple Silicon）
- Windows 10 / 11（x64）
- Android（目前提供开发版 APK，尚未上架）
- iOS：未实现

## 下载与安装

从 [GitHub Releases](https://github.com/nuclearg/dschat/releases) 下载最新版本。

### macOS

文件：`dschat-macos-arm64.dmg`

1. 打开 DMG。
2. 把 `Dschat.app` 拖进「应用程序」。
3. 启动 dschat，菜单栏会出现鲸鱼图标，并自动打开浏览器界面。

macOS 包已使用 Developer ID 签名并完成 Apple 公证，正常情况下不需要右键打开。

如果启动后浏览器没有自动打开，点击菜单栏鲸鱼图标 →「打开」。

菜单栏菜单：

- 打开
- 配置
- 退出

### Windows

文件：`dschat-windows-amd64.zip`

1. 解压。
2. 双击 `dschat-windows-amd64.exe`。
3. 托盘出现鲸鱼图标，浏览器自动打开。

Windows 包目前未做代码签名，SmartScreen 可能提示「未知发布者」；选择「更多信息」→「仍要运行」。

托盘菜单：打开 / 配置 / 退出。

### Android

文件：`dschat-android-debug.apk`

- 最低支持 **Android 8.0（API 26）**
- 当前是 debug 签名，不是应用商店发布包
- 如果覆盖安装时提示签名不一致，先卸载旧版本

安装：

```sh
adb install -r dschat-android-debug.apk
```

也可以把 APK 传到手机后直接安装（需要允许安装未知来源应用）。

App 内使用同一套 Web UI，聊天数据保存在 App 私有目录。

## 第一次使用

### 1. 打开 dschat

桌面端默认监听：

```text
http://127.0.0.1:5217
```

端口固定为 5217。如果端口被其他程序占用，dschat 会启动失败，不会自动换端口。关闭占用端口的程序后重新启动。

### 2. 填入 DeepSeek API Key

首次使用会提示输入 API Key。

1. 打开 [DeepSeek 开放平台](https://platform.deepseek.com/api_keys)。
2. 登录后创建一个 API Key。
3. 回到 dschat，粘贴 Key。
4. 校验通过后会自动保存。

注意：

- API Key 只保存在本机 chatsvr 数据库里，不会发给 sessionsvr。
- 账户需要有余额才能正常请求。
- Key 等同于账户密码，不要发给别人。

也可以在右上角「设置」里更换 API Key。

### 3. 开始聊天

进入主界面后：

- 「开启新对话」新建会话；
- 输入消息后按 Enter 发送；
- 支持深度思考、智能搜索、文件上传、Markdown、代码块和 Mermaid 图表；
- 生成过程中可以停止；
- 回答可以重新生成；
- 问题可以编辑；
- 多版本回答可以在消息旁切换；
- 支持朗读回答；
- 设置里可以切换语言、主题和朗读音色。

## 会话与同步

### 会话保存在哪里

默认在用户目录：

```text
~/.dschat/app/
├── chatsvr.db
├── attachments/
└── api-token
```

Windows 对应用户目录下的 `.dschat\app\`。

删除 App 不会自动删除这些数据。需要完全重置时，先退出 dschat，再删除该目录。

### 侧栏状态图标

连接 sessionsvr 后，会话标题旁会出现状态图标：

| 图标 | 含义 |
|---|---|
| 实心云 | 本设备拥有，已同步 |
| 空心云 | 由其他设备拥有，只读 |
| 时钟 | 仅保存在本机，等待同步 |
| 旋转 | 正在上传或下载 |
| 红色叉 | 存在未同步的本地回答碎片 |
| 琥珀色感叹号 | 同步被阻塞，请检查网络或 sessionsvr 连接 |

未连接 sessionsvr 时不显示这些图标。

### 多端同步（可选）

dschat 的同步中心是 `sessionsvr`，需要自己部署。GitHub Release 里提供了各平台压缩包。

最小启动方式：

```sh
tar -xzf sessionsvr-linux-amd64.tar.gz
cd sessionsvr-linux-amd64

cp config.properties.example config.properties
# 编辑 config.properties，设置 PASSWORD=你的密码

./sessionsvr -addr :5218 -data ~/.dschat/sessionsvr
```

然后在客户端：

1. 打开「设置」；
2. 找到 sessionsvr；
3. 填写服务器地址，例如 `http://192.168.1.10:5218`；
4. 输入 sessionsvr 密码；
5. 点击「连接」。

说明：

- **只同步连接之后新建的会话**；已有的本地会话不会自动上传。
- 会话同一时间只有一个 owner；其他设备打开时为只读。
- 密码为空时任何人都能读写，公网部署必须设置密码，并使用 HTTPS。
- 断开连接不会删除本地会话；切换服务器时，旧服务器上已同步的会话会转成仅本地状态。
- 官方共享服务器尚未开放，目前只支持自建。

公网部署建议：

- sessionsvr 只监听 `127.0.0.1`；
- 前面放 Nginx/Caddy 做 HTTPS；
- 设置强密码；
- 定期备份 `~/.dschat/sessionsvr`。

## 导入 DeepSeek 历史对话

「设置」→「数据管理」→「导入历史对话」。

支持 DeepSeek 官方导出的历史对话 ZIP：

1. 在 DeepSeek 网页端导出历史对话；
2. 在 dschat 选择该 ZIP；
3. 确认导入。

注意：

- 导入会与现有会话**合并**；
- 同一个 ZIP 导入两次会产生两份重复会话；
- 只支持官方导出格式；
- 当前不提供导出功能。

## 删除数据

- 删除单个会话：会话菜单 →「删除」；本地附件会一起删除。
- 删除所有本地对话：设置 → 数据管理 →「删除所有本地对话」。
- 「删除所有」只删除本设备拥有的会话；其他设备拥有的只读会话保留。
- 已同步的会话会同时从 sessionsvr 删除；sessionsvr 软删除，7 天后硬删除。
- 需要彻底重置：退出 dschat，删除 `~/.dschat/app`。

## 常见问题

### 启动后浏览器没有打开

点击菜单栏/托盘图标 →「打开」。

### 端口 5217 被占用

dschat 会直接启动失败。关闭另一个 dschat 实例或占用 5217 的程序后重试。

### API Key 无效 / 余额不足

- 检查 Key 是否复制完整；
- 检查 DeepSeek 账户余额；
- 在设置里更换 API Key。

### 同步一直显示感叹号

表示上传或下载被阻塞。按顺序检查：

1. sessionsvr 是否在运行；
2. 地址和密码是否正确；
3. 网络是否能访问该地址；
4. 服务器磁盘/数据库是否正常。

重新点击「连接」会触发一次对账。

### 会话变成只读

该会话由另一台设备持有。只读设备不能继续追加 turn；需要回到 owner 设备继续。

### Windows 提示未知发布者

Windows 包尚未代码签名，属于预期行为。选择「更多信息」→「仍要运行」。

### macOS 提示无法验证

确认使用的是 v0.1.2 或更新版本；旧版本没有公证。新版本应显示为已公证的 Developer ID 应用。

## 隐私

- API Key、聊天记录、附件默认保存在本机。
- 聊天内容会发送给 DeepSeek API 以生成回答。
- 只有你主动连接 sessionsvr 时，会话才会同步到该服务器。
- dschat 不会把 API Key 发送给 sessionsvr。

## 更新与卸载

- 更新：下载新版本，替换 App/exe；本地数据保留。
- 卸载：删除 App/exe；如需删除数据，再删除 `~/.dschat/app`。
- sessionsvr 数据在 `~/.dschat/sessionsvr`，与客户端数据分开。

## 开发文档

- [开发与构建](docs/development.md)
- [设计文档](docs/design.md)

## License

MIT
