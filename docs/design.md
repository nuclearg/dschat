# DS Chat 设计文档

版本：v0.1（讨论稿）
状态：领域模型、同步协议、抢占语义已定；标注「待定」的条目尚未拍板。
范围：只描述方案，不含实现代码。

---

## 0. 决策清单

| # | 决策 | 状态 |
|---|---|---|
| D1 | chatsvr 是本地状态的唯一权威；web 是纯前端，不持有任何持久状态 | 已定 |
| D2 | 领域模型是「turn 树」：turn = Q + A，每棵树的根是虚拟节点 `"0"` | 已定 |
| D3 | 同步只有两个写动作：`appendTurn(Q)`、`appendAnswer(A)`；没有 update / close / continue | 已定 |
| D4 | owner 是会话级；只有 owner 能写，sessionsvr 拒绝非 owner 的写入 | 已定 |
| D5 | 不用租约、不用 CAS、不用 seq、不允许积压、不做自动重试 | 已定 |
| D6 | 抢占后由新 owner 把遗留的 open turn 用 aborted answer 闭合 | 已定 |
| D7 | 「当前可见路径」是每台设备的本地状态，不同步，视图允许分叉 | 已定 |
| D8 | 不做跨设备实时流；A 完成并同步后其他设备才看到完整回答 | 已定 |
| D9 | DeepSeek API key 只存在 chatsvr，永不下发给 web，也永不发往 sessionsvr | 已定 |
| D10 | 官方共享 key / 网关本期不做；chatsvr 的 API endpoint 保持可配置 | 暂缓 |
| D11 | Android = 同一个 Go 引擎进程内运行 + WebView 承载同一套前端 | 已定 |
| D12 | 桌面 = 单二进制 + 托盘，拉起默认浏览器访问 127.0.0.1 | 已定 |
| D13 | 自建的 sessionsvr 是单租户；多租户属于官方形态，本期不做 | 暂缓 |
| D14 | iOS 只做规划，本期不实现 | 暂缓 |
| D15 | 不支持删除单个 turn/answer，也不支持「续写」 | 已定 |

---

## 1. 目标与非目标

### 目标

1. 本地 Web 版（macOS / Windows）：完整复刻 DeepSeek 官方 chat-demo 的能力，数据落在本地 chatsvr。
2. Android App：同等功能，引擎与桌面共用同一份 Go 代码。
3. sessionsvr：可选的同步中心，Docker 部署，自建单租户。
4. 多端互通：登录某个 sessionsvr 后，会话在设备间同步；所有权可在设备间转移。
5. 无 sessionsvr 时，各端功能完整，数据完全本地。

### 非目标

- 多用户、分享、协作。
- 端到端加密。
- 跨设备实时观看生成过程。
- 官方共享 key、计费、网关（后续再议）。
- iOS（规划中）。
- 删除单条消息 / 单个版本。

### 功能对齐（相对 chat-demo）

| 能力 | 方案 |
|---|---|
| 会话列表、重命名、删除 | 保留 |
| 联网搜索 / 边想边搜 | DeepSeek API 内置工具 `deepseek_web_search` |
| URL 阅读 | `deepseek_read_url` |
| 深度思考 | `thinking: {type: "enabled"}` |
| 附件（图片 / 文档） | DeepSeek Files API + 本地原件 + 可选 blob 同步 |
| 修改问题 | 新增一个兄弟 turn（Q 为修改后的文本） |
| 重新生成 | 新增一个兄弟 turn（Q 复制自原 turn） |
| 停止生成 | 正常闭合 turn，A 带 `stop_reason` |
| 续写 | **不支持** |
| 会话标题 | DeepSeek API 的 `title: {enabled: true}` |
| 语音输入 / 朗读 | ASR / TTS，经 chatsvr 代理 |
| 历史导入 / 导出 | 保留 ZIP 格式，由 chatsvr 处理（待定细节） |
| Markdown / 图表 / 公式 / 代码高亮 | 保留前端实现 |
| 中英文、明暗主题 | 保留 |

---

## 2. 名词

| 名词 | 定义 |
|---|---|
| **session** | 一棵 turn 树 + 一个 owner + 标题。同步与抢占的单位。 |
| **turn** | 一次问答在树上的节点：`{id, parentTurnId, q, createdAt}`，外加最多一条 answer。 |
| **Q** | 用户问题：文本 + 附件引用。 |
| **A** | AI 回答：`blocks`（thinking / 工具块 / 正文）、`usage`、`status`、`stopReason`、模型与开关快照。 |
| **root** | 每个 session 的虚拟根节点，id 固定为 `"0"`，不落库、不同步，永久视为 closed。 |
| **open turn** | 还没有 answer 的 turn。语义是「已提问、尚未回答」。 |
| **closed turn** | 已有 answer 的 turn。只有 closed turn 可以作为父节点。 |
| **版本** | 同一个 `parentTurnId` 下的多个 turn。 |
| **owner** | 会话级：唯一允许 append 的设备。 |
| **可见路径** | 从 `"0"` 出发、沿每台设备本地选择的 child 走出的 turn 链。不同设备可以不同。 |

---

## 3. 总体架构

```
┌──────────────── 设备（mac / win / android）────────────────┐
│  Web 前端（同一套静态资源）                                 │
│        │ HTTP + SSE（本地回环，带 token）                   │
│        ▼                                                   │
│  chatsvr（Go）                                              │
│   ├─ turn 树 / 可见路径 / 草稿                              │
│   ├─ turn 引擎：请求组装、流式解析、落库、闭合               │
│   ├─ DeepSeek 客户端 ────────────────▶ api.deepseek.com     │
│   ├─ 附件存储（本地原件）                                    │
│   ├─ keychain（API key）                                    │
│   └─ sync 客户端 ──┐                                        │
└────────────────────┼───────────────────────────────────────┘
                     │ HTTPS + SSE
                     ▼
              sessionsvr（Go / Docker）
               ├─ devices（配对、token）
               ├─ sessions（owner）
               ├─ turns / answers（树）
               ├─ blobs（可选）
               └─ SSE 广播
```

- 桌面与 Android 运行**同一个 Go 引擎**，区别只在壳。
- chatsvr 是本地唯一权威；sessionsvr 是同步副本，不保存「进行中」的任何状态。
- 官方共享 key 未来若要做，是在 chatsvr 的 API endpoint 前面加一个独立网关，不改变本文任何协议。

---

## 4. 领域模型

### 4.1 树的形状

```
"0"（虚拟根）
 ├── T1 (Q1, A1)
 │    └── T2 (Q2, A2)
 │         ├── T3  (Q3, A3)
 │         ├── T3' (Q3, A3')      ← 重新生成
 │         └── T3''(Q3, A3'')
 └── T1' (Q1', A1')               ← 编辑第一轮问题
```

- 每个 turn 只有一个 `parentTurnId`；`"0"` 是唯一没有实体行的父节点。
- 树只在叶子方向增长，永不移动、永不删除单个节点。
- 「重新生成」与「编辑问题」是同一个操作：在同一个父节点下追加一个新 turn；区别只是 Q 是否被修改。
- 「续写」不支持：turn 一旦有 answer 就不可变。

### 4.2 turn 与 answer

```
turn   = { id, parentTurnId, q, createdAt }
q      = { text, attachments[], createdAt }
answer = { status, stopReason, blocks[], usage, model, thinking, search, title?, createdAt }
```

- `status`：`done` | `aborted`。
  - `done`：正常结束（`end_turn` / `max_tokens` / `refusal`）或用户主动停止；`blocks` 可以是部分内容。
  - `aborted`：没有产出的闭合（被抢占、进程崩溃、放弃）；`blocks` 通常为空。
- 一个 turn 最多一条 answer；写入后不可变。
- answer 的 `title` 是 DeepSeek API 生成的会话标题，用于更新 session 标题。

### 4.3 三条不变量

1. 父节点必须是 `"0"`，或者是一个已有 answer 的 turn。
2. 一个 turn 最多一条 answer。
3. answer 一旦写入不可变。

这三条同时约束 chatsvr 和 sessionsvr。

---

## 5. 存储

### 5.1 chatsvr（本地 SQLite）

```
sessions(id, title, owner_device_id, sync_state, created_at, updated_at)
turns(id, session_id, parent_turn_id, q_json, created_at)
answers(turn_id PK, a_json, sync_state, created_at)
selections(session_id, parent_turn_id, child_turn_id)   -- 本地可见路径，不同步
devices(id, name, platform, server_url)                 -- device token 存 keychain
settings(key, value)
attachments(id, session_id, name, mime, size, path, deepseek_file_id)
local_output(turn_id PK, session_id, blocks_json, reason, created_at)   -- 红色残片
```

- `sync_state`：`local` | `synced` | `pending`。
- `local_output` 只用于展示被抢占时没来得及同步的 A，永不参与同步。
- API key 不进数据库，进 OS keychain。

### 5.2 sessionsvr

```
devices(id, name, platform, token_hash, created_at, last_seen_at)
sessions(id, title, owner_device_id, created_at, updated_at)
turns(id, session_id, parent_turn_id, q_json, created_at)
answers(turn_id PK, a_json, created_at)
blobs(id PK, size, mime, name, path)   -- 可选
```

- `"0"` 不建行。
- 没有 turn 状态机字段：open 就是「没有 answer 行」。
- 索引：`turns(session_id, parent_turn_id)`、`turns(parent_turn_id)`。

---

## 6. 同步协议

### 6.1 配对与认证

- sessionsvr 启动时生成一次性配对码（6 位，10 分钟有效），打印到日志或通过 CLI 获取。
- chatsvr 提交 `POST /v1/pair {code, name, platform}`，换回 `{deviceId, token}`。
- 之后所有请求带 `Authorization: Bearer <token>`。
- 没有账号体系；一个 sessionsvr 只服务一个用户，但可以有多个设备。
- 设备 id 在首次启动时生成并持久化。重装等于一台新设备，旧会话对它只读，需要 claim 抢回。
- 传输必须 HTTPS（局域网自签证书需在客户端显式信任；开发时可关闭校验）。

### 6.2 接口

```
POST   /v1/pair
GET    /v1/meta

POST   /v1/sessions                     创建会话（owner = 调用方）
GET    /v1/sessions                     摘要列表
GET    /v1/sessions/{id}                整棵树
PATCH  /v1/sessions/{id}                改标题（owner）
DELETE /v1/sessions/{id}                删除（owner）

POST   /v1/sessions/{id}/turns          追加 turn（Q）
POST   /v1/sessions/{id}/turns/{tid}/answer   追加 answer
POST   /v1/sessions/{id}/claim          抢占 owner

GET    /v1/stream                       SSE 通知
POST   /v1/blobs                        上传附件原件（待定）
GET    /v1/blobs/{id}
```

### 6.3 关键请求

**追加 turn**

```json
POST /v1/sessions/{id}/turns
{
  "turn": {
    "id": "01J...",
    "parentTurnId": "01J...",
    "q": {
      "text": "...",
      "attachments": [
        {"blobId": "sha256:...", "name": "a.pdf", "mime": "application/pdf", "size": 12345}
      ],
      "createdAt": 1763500000000
    }
  }
}
```

sessionsvr 校验：`owner == 调用方`；`parentTurnId` 为 `"0"` 或同会话且已有 answer 的 turn；`turn.id` 未存在。
返回 `200 {turnId}`。

**追加 answer**

```json
POST /v1/sessions/{id}/turns/{tid}/answer
{
  "answer": {
    "status": "done",
    "stopReason": "end_turn",
    "blocks": [ ... ],
    "usage": { ... },
    "model": "deepseek-flash",
    "thinking": true,
    "search": false,
    "title": "..."
  }
}
```

sessionsvr 校验：`owner == 调用方`；turn 属于该会话；turn 还没有 answer。
返回 `200`。重复提交同一条 answer（例如响应丢失后手动重试）按幂等处理：内容一致返回 `200`，不一致返回 `409 ANSWER_EXISTS`。

**抢占**

```json
POST /v1/sessions/{id}/claim
{ "force": false }
```

- 若 owner 已是自己：直接 `200`。
- 若会话存在 open turn 且 `force == false`：`409 OPEN_TURN`，并带上 `turnId`。
- `force == true`：原子地把 owner 改成调用方，返回 `200`。

**SSE**

```
event: hello        {protocol, serverId}
event: session      {id, ownerDeviceId, updatedAt}
event: turn         {sessionId, turnId, parentTurnId}
event: answer       {sessionId, turnId}
event: deleted      {sessionId}
```

事件只做通知，不带树内容；客户端收到后按需 `GET /sessions/{id}`。

### 6.4 错误码

| code | 含义 |
|---|---|
| `UNAUTHORIZED` | token 无效 |
| `NOT_OWNER` | 调用方不是会话 owner |
| `SESSION_NOT_FOUND` | 会话不存在 |
| `TURN_NOT_FOUND` | turn 不存在 |
| `TURN_EXISTS` | turn id 已存在且内容不同 |
| `PARENT_INVALID` | 父节点不存在或不属于本会话 |
| `PARENT_OPEN` | 父节点还没有 answer |
| `ANSWER_EXISTS` | 该 turn 已有 answer 且内容不同 |
| `OPEN_TURN` | claim 时存在未闭合的 turn |
| `PROTOCOL_MISMATCH` | 协议版本不兼容 |

### 6.5 顺序、幂等、积压

- **不并发**：chatsvr 对同一个 session 的写操作串行发送，前一个成功后才发下一个。
- **不积压**：A 没有同步成功之前，不允许开始下一个 turn。sessionsvr 会用 `PARENT_OPEN` 拒绝挂在这个 turn 下的新 turn；正常路径是 chatsvr 在 A 处于 pending 时直接禁用输入，不发那个注定失败的请求。
- **不自动重试**：失败即停，由用户手动重试。重试依赖 turn id / answer 的唯一约束做幂等。
- **顺序**：兄弟节点按 `createdAt` 排序，相同则按 id 排序。跨设备接管后可能有秒级时钟差，接受。

---

## 7. 回合生命周期

### 7.1 开始

1. web 调 `POST /api/sessions/{id}/turns`，带 Q 与附件。
2. chatsvr 在本地建 turn（open），把当前可见路径的叶子设为它。
3. 如果是同步会话：首次发问前先 `POST /v1/sessions` 创建会话，再 `POST /v1/turns` 上报 Q。任何一步失败都不发起生成，界面提示重试。
4. chatsvr 组装上下文（从 `"0"` 沿可见路径到新 turn 的父节点；aborted 的 turn 连同它的 Q 一起跳过，避免出现连续两条 user 消息），上传附件到 DeepSeek Files API，发起流式请求。
5. 生成过程中，chatsvr 通过本地 SSE 把增量推给 web；web 只渲染，不落库。

### 7.2 结束

| 情况 | 本地 | 同步 |
|---|---|---|
| 正常结束 | 写入 answer（done） | `POST answer`；失败则标 `pending`，允许手动重试 |
| 用户停止 | 写入 answer（done + `stop_reason=user_stopped`） | 同上 |
| 请求失败 | 写入 answer（aborted，可带部分 blocks） | 同上 |
| 失去 owner | 停止生成；未上报内容写入 `local_output`（红色残片） | 不再上报；新 owner 负责闭合 |
| chatsvr 进程重启 | 有 pending answer 的 turn 保留待重试；其余 open turn 关成 aborted | 同左 |

**A 的同步失败不丢内容**：answer 先落本地库（`sync_state=pending`），再上报；上报成功改为 `synced`。在 pending 期间禁止开下一个 turn。

**Q 从未上报成功**（turn 只存在于本地）时，允许用户直接删除这个 turn：sessionsvr 没有它，不涉及同步，也不会留下无法闭合的空洞。

### 7.3 开机自检

chatsvr 启动时：

- 对每个会话，若存在本设备所有、且本地也没有 pending answer 的 open turn，补一条 aborted answer 闭合（生成已经不可能恢复）。
- 若有 pending answer，保留并提示「上次回答未同步」，允许重试。

### 7.4 红色残片

只有一种情况产生：turn 已 open、A 已生成或生成中，但本设备失去了 owner。

- A 无法上报，也不再尝试。
- 内容写入 `local_output`，按 turn id 关联，界面标红、只读，可复制或删除。
- 它不是树节点，不参与同步；换设备、重装即消失。
- sessionsvr 对应的 turn 由新 owner 用 aborted answer 闭合。

---

## 8. 所有权与抢占

### 8.1 规则

- owner 是会话级，同一时刻只有一个设备可以写。
- 任何写入先校验 owner；不是 owner 返回 `NOT_OWNER`。
- 接管永远允许，但存在 open turn 时需要显式确认。

### 8.2 抢占时序

```
A（owner）                 sessionsvr                 B（新设备）
   │ 生成中，turn T1 open      │                         │
   │                          │   POST claim {force}     │
   │                          │◀────────────────────────│
   │                          │  校验通过，owner = B     │
   │  SSE: session(owner=B)   │────────────────────────▶│
   │◀─────────────────────────│                         │
   │ abort 上游请求            │                         │
   │ turn T1 仍 open           │                         │
   │ A 本地写 local_output     │                         │
   │                          │   GET /sessions/{id}     │
   │                          │◀────────────────────────│
   │                          │────────────────────────▶│
   │                          │   POST answer(aborted)   │
   │                          │◀────────────────────────│
   │                          │   此时 B 才允许输入       │
```

要点：

- A 收到 `owner_changed` 后**立刻 abort** 对 DeepSeek 的请求，避免继续消耗额度。
- A 不再上报任何内容；已经上报成功的部分不会回滚。
- B 在 claim 成功后、允许用户输入之前，必须把该会话所有 open turn 用 `aborted` answer 闭合。
- 如果 B 在闭合前崩溃，下一个接管者重复该步骤。

### 8.3 抢占确认

- 客户端在发起 claim 前先读取会话（`GET /sessions/{id}`），检查是否存在 open turn。
- 存在则弹窗：「该轮回答尚未完成，接管会中断且不可恢复，是否继续？」
- 不存在则直接 claim。
- 不依据设备在线状态，不引入心跳或租约。

### 8.4 只读与冲突

- 非 owner 的设备：会话只读，不能发问、改名、删除（即之前讨论中的 `conflict` 状态，现在不再需要单独的字段，由 owner 判断直接推导）。
- 原 owner 被抢后：本地会话变为只读；若存在未上报的 A，显示红色残片。
- 原 owner 可以再次 claim 抢回；红色残片仍只在本地。

---

## 9. 可见路径与版本

- 树的形状由 sessionsvr 存储的数据决定；「看哪条分支」由每台设备本地决定。
- 本地为每个节点保存一个选中的子节点（`selectedChild`），不参与同步。
- 默认策略：打开会话时，从 `"0"` 开始，每层选 `createdAt` 最大的子节点；用户手动切过版本后，该层固定，不再自动跟随。
- 版本列表 = 同一父节点下的所有 turn，按 `createdAt` 排序。
- 因为视图不同步，两台设备可以停在不同分支；这是明确接受的。

---

## 10. chatsvr

### 10.1 模块

```
app/core/              # 全平台共用（纯 Go，无 UI 框架、无平台专属 API）
  chatsvr/             # turn 引擎、本地 API、同步客户端、附件
  deepseek/            # Messages / Files 客户端
  model/               # 领域模型、ID
  xdb/                 # SQLite 打开与 pragma
  apierr/  httpx/      # 错误与 HTTP 辅助
  web/                 # 内嵌前端（go:embed）

app/desktop/           # macOS + Windows 共用：单实例、拉起浏览器（托盘后续加）
app/osx/               # darwin：薄 main + 打包资源
app/win/               # windows：薄 main + 打包资源（待做）
app/mobile/             # gomobile 门面，Android + iOS 共用（待做）
app/android/           # Kotlin + Gradle + WebView 壳（待做）
app/ios/               # Swift + Xcode + WKWebView 壳（规划）

sessionsvr/            # 同步服务，单二进制
```

边界：`app/core` 被四个平台共用；`app/desktop` 只服务 mac/win；`app/mobile` 只服务 android/ios；平台目录只放入口和打包资源。

### 10.2 本地 API

```
GET    /api/bootstrap          设备、设置、连接状态、会话摘要
GET    /api/sessions/{id}      整棵树 + 本地可见路径 + 本地残片
POST   /api/sessions           新建会话
PATCH  /api/sessions/{id}      改名
DELETE /api/sessions/{id}

POST   /api/sessions/{id}/turns                发问
POST   /api/sessions/{id}/turns/{tid}/stop     停止
POST   /api/sessions/{id}/turns/{tid}/retry    重新生成（新兄弟 turn）
POST   /api/sessions/{id}/turns/{tid}/edit     修改问题（新兄弟 turn）
POST   /api/sessions/{id}/claim                抢占
POST   /api/sessions/{id}/sync                 手动补传 / 重试
POST   /api/sessions/{id}/upload               把本地会话上传到 sessionsvr（待定）

GET    /api/events                             SSE：增量、状态、错误
POST   /api/attachments                        附件上传
GET    /api/settings  POST /api/settings       设置（key 只写不读）
POST   /api/server/pair  POST /api/server/disconnect
```

### 10.3 本地鉴权

- 只监听 `127.0.0.1`。
- 启动时生成一次性 token，用 `http://127.0.0.1:5217/#t=<token>` 打开浏览器；前端用 token 换 `HttpOnly; SameSite=Strict` Cookie，之后不再出现在 URL。
- 校验 `Origin` / `Host`，拒绝非本机来源与 DNS rebinding。
- 不返回 CORS 头。
- 端口默认 5217；被占用时回退到临时端口，托盘提示里显示实际端口，后续「打开」使用实际端口。

### 10.4 turn 引擎

- 组装上下文：可见路径 → 跳过 aborted 的 turn（Q 和 A 都不进入）→ 转成 Messages API 的块结构。
- 请求参数：模型、`thinking`、`tools`（搜索 / 读链接）、`safety_check`、`title`、`latest_reminder`。
- 解析 SSE：thinking、text、citations、server_tool_use、结果块、usage、title。
- 生成过程只写内存；结束时一次性落库（本地独有会话可边生成边落库）。
- 上游错误分类：网络、HTTP、流中断、协议错误；映射为 answer 的 aborted 或 done + stop_reason。

### 10.5 同步客户端

- 每条会话串行：一个写操作完成后再发下一个。
- 收到 `NOT_OWNER` 视为「已失去所有权」：停止上报、abort 上游、标记只读。
- 收到 `owner_changed` 但仍是自己：忽略。
- 断线：SSE 退避重连；重连后刷新会话摘要与打开中的会话。
- 断线期间不重发写操作；由用户手动重试。

---

## 11. Web 前端

- 以官方 chat-demo 为基线 fork（MIT），保留组件、Markdown、i18n、主题、语音。
- 替换 `services/chat` 的存储与请求层：
  - 删除 IndexedDB；会话与设置全部来自 chatsvr。
  - 流式渲染改为订阅 `/api/events`。
  - 编辑 / 重试 / 停止改为调用本地 API。
- 视图模型：把 turn 树转成消息列表（Q、A 交替），版本选择器按 turn 分组。
- 纯前端不保存持久状态；输入框草稿若要保留，存 chatsvr（待定）。
- 附件：先传 `POST /api/attachments`，由 chatsvr 保存原件并上传 DeepSeek Files API。

---

## 12. sessionsvr

### 12.1 行为

- 只存树、owner、设备、可选 blob。
- 不执行模型请求，不感知「生成中」，没有租约与超时。
- 写操作严格校验 owner。
- 单用户；配对码绑定设备。

### 12.2 部署

- 单二进制，Docker 镜像（多阶段构建，最终 alpine 或 distroless）。
- 数据卷：SQLite 文件 + blob 目录。
- 配置：监听地址、数据目录、TLS（建议由 Caddy/Traefik 终止）、配对码有效期。
- 健康检查：`GET /v1/meta`。
- 备份：停写或用 SQLite 在线备份，连同 blob 目录一起打包。

### 12.3 规模

单用户、几十台设备、几千个会话，SQLite 足够。消息体较大（含 thinking / 工具块），单条上限建议 256KB，超出拒绝。

---

## 13. Android

- 引擎：`gomobile bind` 产出 AAR，进程内起本地 HTTP，监听 `127.0.0.1` 随机端口。
- UI：WebView 加载同一套前端静态资源。
- Kotlin 壳负责：
  - 前台服务保活（生成期间显示通知）；
  - `onShowFileChooser`、录音权限、`onPermissionRequest`；
  - 返回键、分享、通知点击；
  - 存储目录与 keychain（Android Keystore）。
- 网络：`network_security_config` 允许 `127.0.0.1` 明文。
- minSdk 26，要求较新的 WebView。
- 进程被杀：引擎重启后按 §7.3 自检；SSE 重连。
- 已知限制：Doze 下与 sessionsvr 的长连接可能断，恢复时重连；不保证后台持续生成。

---

## 14. 桌面壳

- 单二进制 `dschat`：默认启动引擎 + 托盘；`dschat serve` 无托盘（可跑 Linux）。
- 托盘菜单：打开 / 配置 / 退出。
- 单实例：已有实例时只拉起浏览器，不重复监听。
- 开机自启：可选。
- macOS：`LSUIElement`（仅菜单栏）、universal binary、codesign + notarize、DMG。
- Windows：`-H=windowsgui`、托盘、安装包、代码签名（避免 SmartScreen）。
- 日志：`~/.dschat/logs`，滚动；绝不打印 key 与 Authorization。

---

## 15. 安全

| 项 | 措施 |
|---|---|
| API key | OS keychain；只在 chatsvr 内存中使用；不落库、不出现在日志与错误信息里 |
| key 的出站 | 只发往配置的 DeepSeek API origin；origin 不可由页面指定 |
| 本地 API | 127.0.0.1 + 一次性 token + HttpOnly Cookie + Origin 校验 |
| sessionsvr | HTTPS + Bearer token；配对码一次性；token 只存哈希 |
| 附件 | 本地原件权限 0600；blob 目录按用户隔离 |
| 日志 | 统一脱敏；请求体不落日志 |
| 传输 | web ↔ chatsvr 明文但仅回环；chatsvr ↔ sessionsvr 必须 TLS |

---

## 16. 失败模式

| 场景 | 行为 |
|---|---|
| sessionsvr 不可达 | 同步会话只读（无法开新 turn）；本地会话不受影响 |
| Q 上报失败 | 不发起生成；提示重试 |
| A 上报失败 | 本地标 pending；禁止下一个 turn；允许手动重试 |
| 生成中网络断 | 上游断开 → answer aborted / done + 部分内容；按正常流程上报 |
| 被抢占 | abort 上游；未上报内容转红色残片；只读 |
| chatsvr 崩溃 | 重启后闭合无 pending answer 的 open turn；有 pending 的保留待重试 |
| 设备时钟偏差 | 只影响兄弟版本排序，不影响结构 |
| 两次 claim 并发 | claim 是单事务覆盖写，后到者胜；先到者从 SSE 收到 owner 变更后转只读 |
| 重复提交 turn / answer | 唯一约束 + 幂等返回 |

---

## 17. 里程碑

| 阶段 | 内容 | 预估 |
|---|---|---|
| M0 | chatsvr + web 本地版，功能对齐 demo（无同步） | 4–6 周 |
| M1 | sessionsvr、配对、appendTurn/appendAnswer、只读观察 | 2–3 周 |
| M2 | claim、open turn 闭合、失败重试与自检 | 1–2 周 |
| M3 | 托盘壳、打包、签名 | 1 周 |
| M4 | Android（引擎 AAR + WebView + 前台服务） | 2–3 周 |
| M5 | 官方共享 key / 网关 / 计费（暂缓） | 3–4 周 |
| M6 | iOS（规划） | — |

---

## 18. 待定

1. **附件是否上传原件到 sessionsvr。**
   建议：是，content-addressed blob，按会话懒上传，单文件上限 50MB。否则换设备后附件只剩文件名，且无法用新 key 重新引用。
2. **本地已有会话在登录后如何处理。**
   建议：默认只同步新建会话；已有会话由用户显式「上传到 sessionsvr」，走一次性批量导入接口。
3. **抢占是否弹确认。**
   建议：会话存在 open turn 时弹；否则直接接管。
4. **历史导入 / 导出的边界。**
   导入的会话是本地独有；导出是否包含红色残片、是否包含原件，需要定。
5. **删除会话是硬删还是墓碑。**
   建议：硬删；其他设备在下次拉列表时消失。
6. **草稿是否进 chatsvr。**
   建议：进，避免刷新丢失；草稿不参与同步。
7. **退出登录后，已同步会话怎么处理。**
   建议：默认保留为只读副本；需要继续使用时，提供「复制为本地会话」（新 id，脱离同步）。不允许在已同步会话上继续本地写入，否则会破坏「不积压」。
8. **sessionsvr 不可达时，同步会话能否继续聊。**
   建议：不能，只读。允许本地继续会产生永远同步不出去的 turn，和「不积压」冲突。用户想离线聊，应新建本地会话。

---

## 19. 测试

- **多设备模拟器**：内存版 sessionsvr + N 个 chatsvr 实例，脚本化跑：
  - 正常 append / claim / 抢占时序；
  - claim 与 append 并发；
  - 抢占后 open turn 的闭合；
  - 断线重连、迟到写入被拒；
  - 时钟偏差下的版本排序。
- **协议一致性**：turn 树的构造与序列化 fixture，chatsvr / sessionsvr 共用。
- **幂等**：重复 turn / answer 提交。
- **端到端**：录制 DeepSeek SSE 样本，离线回放，校验 blocks 与上下文组装。
- **Web**：刷新中断、SSE 重连、只读态、红色残片渲染。

---

## 20. 参考

- 官方前端 demo（MIT）：<https://github.com/deepseek-ai/chat-demo>
- 构建聊天应用指南：<https://api-docs.deepseek.com/guides/build_a_chatbot>
- 全家桶应用 API 公告（搜索 / 文件 / URL 读取 / 安全审核 / 语音）：<https://api-docs.deepseek.com/news/news261113>
