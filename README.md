# dschat

跨端 DeepSeek 聊天客户端：一个本地 `chatsvr` 引擎 + 内嵌 Web UI，以及一个可选的 `sessionsvr` 同步中心。桌面端与 Android/iOS 共用同一份 Go 引擎。

当前进度：**官方 demo 前端已接到 chatsvr**（只用它的视觉/交互，不再用它的 IndexedDB/状态机），本地引擎 + sessionsvr 同步已跑通。托盘、Android/iOS 壳、官方网关尚未实现。

![UI](docs/ui.png)

## 现在能做什么

- 本地 chatsvr：会话、turn 树、流式输出、thinking 块、联网搜索/读链接、服务端标题、附件、停止、重新生成、编辑问题、版本切换。
- Web UI（`go:embed` 进单二进制）：上述功能的界面，Enter 发送、SSE 流式渲染、Markdown。
- sessionsvr：配对、会话/回合同步、会话级 owner、跨设备 claim、只读、SSE 通知。
- 两台设备抢同一个会话：旧设备收到 `owner_changed` 后变只读，新设备接管并把遗留 open turn 用 `aborted` 闭合。

## 前端

- `/`：官方 chat-demo 前端（MIT，源码在 `app/core/web/app`），视觉与交互照抄官方。会话、消息、turn 树、设置全部来自 chatsvr；所有 DeepSeek 请求经 `/api/llm/*` 由 chatsvr 注入 key，浏览器不保存 key、也不直接访问任何外部 API。
- `/legacy`：上一版自绘 UI，仍保留，方便对照。
- 注意：由于后端是 turn 模型（重新生成 = 同一父节点下新增一个带 Q 副本的 turn），官方 UI 的版本选择器会显示在「问题」一侧，而不是「回答」一侧；样式一致，位置与官方 demo 略有差异。

## 构建

```sh
make build            # 只编 Go；仓库里已带构建好的 dist
make web              # 改了前端后重新构建 dist（需要 pnpm）
```

`make build` 产物：`bin/dschat`（macOS）、`bin/sessionsvr`。

## 运行

1. 启动 sessionsvr（可选）：

```sh
./bin/sessionsvr -addr :5218 -data ~/.dschat/sessionsvr
```

启动日志里会打印 6 位配对码，10 分钟内有效。配对成功后会自动换一个新码；过期就重启进程。

2. 启动客户端：

```sh
./bin/dschat
```

默认起在 `127.0.0.1:5217`，并拉起默认浏览器。端口被占用时会自动换一个空闲端口。`-no-open` 不打开浏览器，`-port` / `-data` 可覆盖。

3. 首次使用：右上角「设置」→ 填 DeepSeek API Key。Key 只保存在本机，不会发往 sessionsvr。

4. 需要多端同步：设置 → sessionsvr 地址 + 配对码 → 连接。连接后**新建**的会话会同步；已有的本地会话不会自动上传（这是设计里待定的行为）。

## 目录

```
app/core/          # 全平台共用（纯 Go）
  chatsvr/         # turn 引擎、本地 API、同步客户端、附件
  deepseek/        # Messages / Files 客户端
  model/ xdb/ apierr/ httpx/
  web/             # 内嵌前端：dist/（官方 demo 构建产物）+ legacy/（旧版 UI）+ app/（demo 源码）
app/desktop/       # macOS + Windows 共用的启动逻辑（目前是起引擎 + 开浏览器）
app/osx/           # darwin main
app/win/           # windows（待做）
app/mobile/        # gomobile 门面，Android + iOS 共用（待做）
app/android/       # Kotlin + Gradle + WebView（待做）
app/ios/           # Swift + Xcode（规划）
sessionsvr/        # 同步服务，单二进制
docs/design.md     # 设计文档
```

边界约定：`app/core` 不出现平台专属 import；`app/desktop` 只服务 mac/win；`app/mobile` 只服务 android/ios；平台目录只放入口与打包资源。

## 数据

- 客户端：`~/.dschat/app/`（`chatsvr.db`、`attachments/`、`api-token`）
- 同步服务：`~/.dschat/sessionsvr/`（`sessionsvr.db`）

目录权限 0700，数据库/附件 0600。`-data` 可改。

## 已验证

- 用 mock DeepSeek SSE 跑通：thinking/text/signature/usage/title → 本地落库 → 推送 sessionsvr。
- 重新生成产生兄弟 turn；切版本会改变可见路径。
- 双设备 claim：旧设备 SSE 收到 owner 变更 → 只读；旧设备发问返回 `READ_ONLY`。
- 同一 chatsvr 双窗口：B 的列表通过 SSE 自动出现新会话；生成中打开时 chatsvr 返回内存中的部分输出，B 能看到已生成的前缀并继续接收后续 SSE。
- 两个 chatsvr 连同一 sessionsvr：B 的列表自动出现新会话，打开后显示等待点（只读）；A 完成后 B 自动刷新出完整回答。
- 同一 session 两个窗口并发发 turn：chatsvr 用 `sending`/`active` 原子占位，只有一个请求返回 200，另一个返回 409 `TURN_IN_PROGRESS`；不会产生两个 open turn。
- 抢占者闭合遗留 open turn（`aborted`），本地与 sessionsvr 一致。
- 上下文组装会跳过 aborted turn，且只回传 text + read_link 工具块。
- UI E2E（Playwright + 本机 Edge）：发消息、流式渲染、设置弹窗，无 console error。
- `go test ./...` 覆盖可见路径、上下文链、sessionsvr 的 append/claim/open-turn 规则。

## 已知缺口

- API key 和 sessionsvr token 目前明文存在本地 SQLite（目录 0700），keychain 接入未做。
- 附件原件未同步到 sessionsvr，只同步元数据；换设备后无法用新 key 重新引用。
- 托盘图标、单实例锁、macOS 签名/公证、Windows 壳、安装包未做。
- Android/iOS/mobile façade 未做。
- 官方 UI 已把非 owner 的会话置为只读（输入框禁用、显示等待点），但还没有「接管」按钮；消息上的重新生成等操作仍可能发出 `READ_ONLY` 请求。
- 跨 chatsvr 的生成过程只同步「正在思考」状态，不逐 token 同步；回答在 turn 完成后才出现在另一台设备。
- 官方 UI 的历史导入/导出仍走 demo 原来的 ZIP/IndexedDB 逻辑，尚未接到 chatsvr。
- 附件上传已接到 chatsvr，但跨设备附件原件同步（blob）还没做，端到端也还没验证。
- 官方 UI 的版本选择器显示在问题一侧（turn 级映射），与官方回答侧的交互位置不同。
- 官方共享 key / 网关 / 计费未做。
- 不支持删除单条 turn / 单个版本，不支持「续写」（设计决定）。
- 配对码过期后需要重启 sessionsvr 才能拿到新码。

## 设计

完整方案见 [docs/design.md](docs/design.md)。核心模型：session 是一棵 turn 树，turn = Q + A，虚拟根 `"0"`；同步只有 `appendTurn(Q)` 和 `appendAnswer(A)` 两个动作。
