# 服务端插件安装

运行包内含两个 DLL，每个游戏服只安装与加载器对应的一个。它们均独立包含桥接逻辑，无需给服务器额外放 Newtonsoft.Json 或 WebSocket 第三方 DLL。EXILED 9.14.2 基于 LabAPI，因此两套插件共用当前 LabAPI 游戏包装接口。

## LabAPI 1.1.7

默认 Windows 目录示例，游戏端口以 `31140` 为例：

```text
DLL: %APPDATA%\SCP Secret Laboratory\LabAPI\plugins\31140\QQBotLite.LabApi.dll
配置: %APPDATA%\SCP Secret Laboratory\LabAPI\configs\31140\QQBotLite.LabApi\config.yml
```

所有端口均安装时也可放 `plugins/global`，各端口仍分别配置。启用 `gamedir_for_configs` 的服使用游戏工作目录下的 `AppData`，遵循 LabAPI 加载器实际生成的位置。[LabAPI 路径实现](https://github.com/northwood-studios/LabAPI/blob/1.1.7/LabApi/Loader/Features/Paths/PathManager.cs)、[配置目录实现](https://github.com/northwood-studios/LabAPI/blob/1.1.7/LabApi/Loader/ConfigurationLoader.cs)

启动一次让框架生成配置，停止游戏服后填入机器人生成的对应端口配置。内容如下，Token 替换成实际值：

```yaml
is_enabled: true
debug: false
bot_url: 'ws://127.0.0.1:17890/scpsl'
server_id: '31140'
server_name: '1服'
token: '填写机器人引导生成的 Token'
enable_help: true
```

保持插件目录下 `properties.yml` 的启用设置，然后重启游戏服。

## EXILED 9.14.2

将 `QQBotLite.Exiled.dll` 放入 `%APPDATA%\EXILED\Plugins`，启动一次，停止游戏服后，在 `%APPDATA%\EXILED\Configs\31140-config.yml` 中编辑 `qq_bot_lite` 段。已有其他插件配置保留，只改这一段。[EXILED 配置说明](https://github.com/ExMod-Team/EXILED/blob/master/.github/documentation/README.md)

```yaml
qq_bot_lite:
  is_enabled: true
  debug: false
  bot_url: 'ws://127.0.0.1:17890/scpsl'
  server_id: '31140'
  server_name: '1服'
  token: '填写机器人引导生成的 Token'
  enable_help: true
```

然后重启游戏服。非默认 EXILED 路径使用服务端实际生成的配置文件。

## 检查连接

机器人与插件的 `server_id`、Token 必须一致。多个游戏服使用不同 ID；同一个 ID 同时只能连接一个插件。首次空 Token 时插件会显示配置提示，不连接机器人。配置好后两端分别显示连接信息，机器人显示“已就绪”。

游戏访问在 Unity 主线程执行，网络连接在后台执行；连接失败会自动重连。管理操作不会自动重发，重复请求 ID 仅返回原结果。插件内部请求过期后不执行；已断开的旧连接上的排队操作也不执行。

`.ac` 在玩家控制台中输入。管理消息显示为控制台文字与 Hint；求助使用服务器名称、玩家昵称和内容，不需要 QQ 绑定或数据库。广播自动转义富文本标签。

`allrest` 调用服务端的重启 API；重新拉起进程由 SCP:SL 的服务端启动器负责。用 LocalAdmin 等能管理重启的启动方式运行游戏服。

插件已针对本机 LabAPI 1.1.7 与 EXILED 9.14.2 的实际程序集编译。不同游戏版本导致接口变动时，用对应服务器的 Managed 目录重新编译，见开发说明。
