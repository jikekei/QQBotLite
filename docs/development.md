# 编译与开发

## 工具

- Go 1.26 或以上，本次使用 Go 1.27.1。机器人仅依赖 `github.com/gorilla/websocket v1.5.3`，其余使用标准库。
- .NET SDK 10，用于构建插件与跨语言测试工具。
- .NET Framework 4.8 Developer Pack，用于插件 `net48` 引用程序集。
- 对应 SCP:SL 服务端 `SCPSL_Data/Managed` 目录，含 LabApi.dll、游戏和 Unity 程序集。
- EXILED 9.14.2 的 Exiled.API.dll 所在目录，来自 [EXILED 官方发布包](https://github.com/ExMod-Team/EXILED/releases/tag/v9.14.2)。这些框架 / 游戏 DLL 仅用作编译引用，不进入分发包。

Windows PowerShell 在项目根目录执行：

```powershell
.\build.ps1 -ScpslManaged 'D:\SCPSL\SCPSL_Data\Managed' -ExiledRefs 'D:\References\Exiled9142'
```

Go 在 PATH 或本项目 `.tools/go/bin/go.exe` 中。可用 `-Go 'D:\Go\bin\go.exe'` 指定。脚本先编译两套插件和测试工具、运行 Go vet 与测试，再构建无 CGO 的 Windows x64 单文件程序，生成运行包、源码包和 SHA-256 校验清单。测试工具的 .NET 10 运行时仅用于开发，不是机器人运行依赖。

输出目录 `dist/QQBotLite-win-x64`、`dist/QQBotLite-win-x64.zip`、`dist/QQBotLite-source.zip`。源码包明确排除 `.tools`、编译中间文件、真实配置、Token 与日志。构建过程不安装游戏插件，不改服务端原配置。仅打包可运行 `package.ps1`；`-SkipTests` 可跳过开发测试，不应替代正常验证。

## 测试

```powershell
dotnet build tests\PluginHarness\PluginHarness.csproj -c Release
$env:QQBOTLITE_PLUGIN_HARNESS = (Resolve-Path tests\PluginHarness\bin\Release\net10.0\PluginHarness.dll).Path
go -C bot test ./... -count=1
```

Go 测试用本机临时 HTTP / WebSocket 接口模拟 QQ，不登录真实 QQ。`PluginHarness` 编译链接生产插件的 `Contracts.cs` 和 `BridgeClient.cs`，使用模拟游戏接口，通过真实网络与 Go 桥接通信，验证所有游戏操作在主线程执行、重复 ID 不重复踢人、求助限频及重启回应顺序。未设置环境变量时此项跳过，其余测试正常执行。`build.ps1` 还会设置 `QQBOTLITE_BINARY`，验证生成的 EXE 的首次引导、启动和完整消息链路。

## 目录

| 目录 / 文件 | 用途 |
| --- | --- |
| `bot` | 中文配置引导、QQ 适配器、权限、指令、游戏桥接与测试 |
| `plugins/Shared` | 两种插件共享的协议、连接、请求去重及游戏接口 |
| `plugins/LabApi` | LabAPI 加载器入口与 `.ac` |
| `plugins/Exiled` | EXILED 加载器入口与 `.ac` |
| `tests/PluginHarness` | 不依赖游戏运行时的实际插件桥接测试 |
| `scripts` | 随运行包提供的启动、配置、检查脚本 |
| `docs` | 安装、QQ 接入、协议、开发和验证说明 |

目前不加入 Web 管理面板、数据库、机器人框架全家桶、聊天自动回复服务或原来的硬件状态采集服务。
