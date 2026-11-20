# 开发与构建

本文给开发和发布使用；普通使用见 [使用指南](../README.md)。

## 环境要求

- Go 1.26 或更新版本
- Node.js 22 + pnpm 9（只在修改 Web UI 时需要）
- macOS 构建：Xcode Command Line Tools
- DMG 布局：`python3 -m pip install --user dmgbuild`（或设 `DMGBUILD_PYTHON` 指向装有 dmgbuild 的 Python）
- Windows 构建：`mingw-w64`
- Android 构建：Android SDK、NDK 27.2、gomobile

macOS 发布包最低支持 **macOS 13 Ventura**。当前工具链使用 Go 1.27 时最低就是 13；如果要用 Go 1.26 构建并保持 13 也是允许的。

## 常用命令

```sh
make build            # Go 二进制：bin/dschat、bin/sessionsvr
make web              # 重新构建 Web UI 到 app/core/web/dist
make shells           # macOS .app + Windows .exe
make app-macos        # dist/Dschat.app
make app-macos-dmg    # 本机签名并打 DMG
make app-windows      # dist/dschat-windows-amd64.exe
make android          # dist/android/dschat-debug.apk + chatsvr.aar
```

`make shells` 会构建 universal macOS app（arm64 + x86_64）和 Windows exe。  
`make app-macos-dmg` 默认构建 arm64 DMG，使用本机钥匙串里的 Developer ID 证书签名。

## macOS 签名与公证

本机构建：

```sh
make app-macos-dmg
```

如果有 App Store Connect API Key，可以同时完成公证：

```sh
APPLE_API_KEY="$HOME/.appstoreconnect/private_keys/AuthKey_XXXX.p8" \
APPLE_API_KEY_ID=XXXXXXXXXX \
APPLE_API_ISSUER_ID=xxxxxxxx-xxxx-xxxx-xxxx-xxxxxxxxxxxx \
./scripts/build-macos-dmg.sh
```

脚本会：

1. 构建 arm64 app；
2. codesign，开启 Hardened Runtime；
3. 创建并签名 DMG；
4. `notarytool submit --wait`；
5. `stapler staple`；
6. 校验 DMG。

默认使用区域 S3 端点上传（`--no-s3-acceleration`），国内网络更稳定。需要 S3 Transfer Acceleration 时设置：

```sh
NOTARY_S3_ACCELERATION=1
```

## 构建发布包

```sh
./scripts/build-release.sh
```

产物在 `dist/release/`：

- `dschat-macos-arm64.dmg`
- `dschat-windows-amd64.zip`
- `dschat-android-release.apk`
- `sessionsvr-darwin-arm64.tar.gz`
- `sessionsvr-linux-amd64.tar.gz`
- `sessionsvr-linux-arm64.tar.gz`
- `sessionsvr-windows-amd64.zip`
- `SHA256SUMS`

`sessionsvr` 包内包含 `config.properties.example`。

## 发布流程

推 `v*` tag 会自动创建 GitHub Release：

```sh
git tag v0.1.4
git push origin v0.1.4
```

Release workflow 会：

- 运行测试、类型检查和 lint；
- 构建 Web；
- 构建四平台 sessionsvr；
- 构建 Windows app；
- 构建并签名 Android release APK（minSdk 26）；
- 导入仓库 Secrets 中的 Developer ID 证书；
- 构建、签名并公证 macOS DMG；
- 创建 GitHub Release 并上传全部包和 `SHA256SUMS`。

需要的 GitHub Secrets：

```text
APPLE_CERTIFICATE_P12_BASE64
APPLE_CERTIFICATE_PASSWORD
APPLE_SIGNING_IDENTITY
APPLE_TEAM_ID
APPLE_API_KEY_BASE64
APPLE_API_KEY_ID
APPLE_API_ISSUER_ID
```

## CI

- `.github/workflows/ci.yml`：push main / PR 时运行测试和 Web 构建。
- `.github/workflows/test.yml`：可复用的 Go 测试和前端检查。
- `.github/workflows/build-web.yml`：可复用的 Web 构建。
- `.github/workflows/release.yml`：tag 发布，也支持手动触发构建（手动触发不会创建 Release）。

## Android 签名

本地签名使用 `~/.android-keys/dschat-release.properties`，其中指向 release keystore。
CI 使用以下 Secrets 导入 keystore：

```text
ANDROID_KEYSTORE_BASE64
ANDROID_KEYSTORE_PASSWORD
ANDROID_KEY_ALIAS
ANDROID_KEY_PASSWORD
```

产物为 `dist/android/dschat-android-release.apk`。没有本地 keystore 时，脚本回退到 debug APK，仅用于开发。

## 测试

```sh
go vet ./...
go test ./...
go test -race ./app/core/... ./sessionsvr

cd app/core/web/app
pnpm install --frozen-lockfile
pnpm type
pnpm lint
```

## 目录结构

```text
app/core/
  chatsvr/         turn 引擎、本地 API、同步客户端、附件
  deepseek/        DeepSeek Messages / Files 客户端
  model/ xdb/ apierr/ httpx/
  web/             内嵌前端：dist/（构建产物）+ app/（demo 源码）

app/desktop/       macOS + Windows：引擎启动、菜单栏/托盘、打开浏览器
app/osx/           macOS 入口
app/win/           Windows 入口与 exe 资源
app/mobile/        gomobile 门面，Android/iOS 共用
app/android/       Kotlin + Gradle + WebView + 前台 Service
app/ios/           Swift + Xcode（规划）
sessionsvr/        同步服务，单二进制
scripts/           构建、签名、打包脚本
docs/              使用、开发与设计文档
```

约定：

- `app/core` 不出现平台专属 import；
- `app/desktop` 只服务 macOS/Windows；
- `app/mobile` 只服务 Android/iOS；
- 平台目录只放入口和打包资源。

## 数据与兼容性

- 客户端数据：`~/.dschat/app/`（`chatsvr.db`、`attachments/`、`api-token`）
- 同步服务数据：`~/.dschat/sessionsvr/`
- 目录权限 0700，数据库/附件 0600。

协议和数据库结构是 **v1**：`protocol=1`，chatsvr/sessionsvr schema 均为 v1。不保留旧库迁移和旧协议兼容；升级时必须使用新数据目录或重新导入。

## 设计

完整领域模型和同步协议见 [design.md](design.md)。
