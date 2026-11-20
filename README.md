# dschat

原汁原味复刻 DeepSeek 网页版和 App 体验！

不受限制的重新生成次数！

不受限制的编辑问题次数！

## 下载与安装

从 [GitHub Releases](https://github.com/nuclearg/dschat/releases) 获取最新版本，请根据您的实际情况下载：

* `dschat-macos-arm64.dmg` macOS 版，要求 macOS 13 Ventura 及以上（Apple Silicon）
* `dschat-windows-amd64.exe` Windows 版，要求 Windows 10 / 11（x64）
* `dschat-android-release.apk` Android 版，要求 Android 8.0 及以上
* iOS 版敬请期待

## 本地使用

启动后会自动弹出对话界面，同时常驻菜单栏图标。

您需要自备 DeepSeek 的 API Key。具体可 [参考这里](https://www.bing.com/search?q=%E5%A6%82%E4%BD%95%E7%94%B3%E8%AF%B7deepseek%E7%9A%84api+key&PC=U316&FORM=CHROMN)。

## 多端数据同步

默认所有会话只存在本机，不会上传任何数据。

如果想在电脑、手机之间共用同一份会话，可以自己部署一个同步服务 `sessionsvr`：
它只有一个可执行文件加一个配置文件，会话和附件都留在你自己的机器上，不经过第三方服务。

部署步骤、配置说明、HTTPS、备份与升级都在
[自建数据同步服务部署指南](docs/self-host.md) 里。

## 意见和建议

通过 [GutHub Issues](https://github.com/nuclearg/dschat/issues) 提交

也可以加入 QQ 群

[](https://github.com/nuclearg/dschat/blob/main/docs/qq.png?raw=true)

## 常见问题

### 是免费的吗？

不免费。DeepSeek 的 API Key 是需要充值才能使用的。

其它待补充
