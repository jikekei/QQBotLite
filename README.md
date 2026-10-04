# 幻梦 QQBotLite

独立、轻量的 SCP: Secret Laboratory QQ 机器人。机器人使用 Go，游戏插件使用 C#；机器人与插件通过带 Token 的 WebSocket / JSON-RPC 2.0 通信。

提供 Windows x64 单文件程序、LabAPI 1.1.7 插件和 EXILED 9.14.2 插件。运行机器人无需安装 Go、Python、Node.js、数据库或额外服务。NapCat 与官方 API 在配置中二选一。

## 三步启动

1. 解压运行包，双击 `start.cmd`，按中文引导填写 QQ 接入方式、游戏端口、服务器名及需要的群 / 管理员。程序生成 `config.json` 和每个游戏服的 `plugin-端口.yml`。下一次双击会直接启动。回车保留默认值，输入 `-` 清空可选项。
2. 从 `plugins/LabAPI` 或 `plugins/EXILED` 选择一个 DLL，装到对应游戏服。启动一次让框架生成配置，再按生成的 `plugin-端口.yml` 填写 `bot_url`、`server_id`、`token`。两种 DLL 每个服只装一个。
3. 重启游戏服。机器人显示“游戏服已就绪”后，在 QQ 中发送 `/cx`。官方群机器人按平台接收方式先 @ 机器人再输入指令。

重新填写配置用 `configure.cmd`；仅检查配置用 `check-config.cmd`。查看 [插件安装说明](docs/plugins.md) 和 [QQ 接入说明](docs/qq-adapters.md)。

## 指令

| 输入 | 行为 |
| --- | --- |
| `/cx` | 按配置顺序查询所有服务器人数、在线管理员、总人数 |
| `/info` | 查询所有服务器 D 级人员、博士、SCP、回合时间、回合次数和下一波刷新倒计时 |
| `/列表`、`/＃` | 查询第 1 服玩家名与游戏内 ID |
| `/列表 2`、`/＃ 2` | 查询第 2 服玩家列表 |
| `/服务器状态`、消息包含 `炸了？` 或 `炸了?` | 查询状态；配置 `status_image` 后附带已有探针图片 |
| 消息包含 `helloworld` | 回复 `hello world！` |
| `round list` | 第 1 服玩家列表，管理指令权限规则仍适用 |
| `round start` | 开启第 1 服回合 |
| `round rest` | 重启第 1 服当前回合 |
| `round allrest` | 重启第 1 服服务端进程 |
| `round kick+17` | 踢出第 1 服玩家 ID 17，不写入封禁列表 |
| `round bc+公告内容` | 向第 1 服广播 |
| `round 2 kick+17` | 对第 2 服执行操作；其他操作同样可加序号 |
| 游戏内 `.ac 求助内容` | 提示在线管理，并向配置的 QQ 目标发送求助 |

`/round` 与 `round` 等价。省略序号只操作第 1 服，`allrest` 的含义是重启选中的服务端进程。回合与进程重启返回“已受理”；随后执行重启，最终恢复情况通过 `/cx` 查询。

按照精简范围移除了 `/绑定`、`/击杀榜`、`/查询击杀信息` 及其数据库；没有新增 QQ 指令。状态图片读取现有文件，不运行原项目的浏览器截图与硬件监控服务。插件断开与已连接的空服分别显示。

## 权限与通知

- `administrator_ids` 非空：只有名单内的用户可以执行 `round`，群管理员身份不额外授权。
- 名单为空：允许当前群管理员和群主；普通成员无管理权限。私聊或平台未提供群身份时，需要填写管理员 ID。
- NapCat 使用数字 QQ 号、群号。官方 API 使用实际事件中的用户 / 群 OpenID，不填写数字 QQ 号。机器人日志会显示会话 ID、用户 ID 与群身份，不输出票据和聊天正文。官方群身份使用当前文档中的 `author.member_role`；缺少该字段时按未提供身份处理。[官方事件说明](https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_at_message_create.html)
- `allowed_groups` 为空时允许所有群；非空时只处理列出的群，也只向这些群推送求助。私聊不受群白名单影响。
- `alert_groups` / `alert_users` 为空时不发对应 QQ 通知；游戏内管理提示仍可用。`.ac` 每名玩家间隔 30 秒，最多 500 字，可通过插件 `enable_help: false` 关闭。
- 平台拒绝、账号缺少权限、群未允许消息、连接断开或发送超时：这次消息立即丢弃，不缓存、不重发、不转发到其他会话、不在下一条查询中补发。状态文字发送失败也不再发送图片。官方发送权限仍取决于应用权限、群授权和平台限制。[官方消息说明](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)

## 配置与部署

首次引导为每个游戏服生成独立随机 Token，机器人和插件需完全一致。示例 `config.example.json` 仅供查看字段，请通过引导生成真实配置。配置修改后重启机器人；重新配置脚本保存后会启动机器人。

默认机器人监听 `127.0.0.1:17890`，插件连接 `ws://127.0.0.1:17890/scpsl`；同机部署不用改。多服的 `server_id` 分别填对应端口；服务器序号按 `servers` 数组顺序确定。

分机部署时修改 `bridge_listen` 为可达地址，插件 `bot_url` 指向机器人。公网连接用 HTTPS 反代的 `wss://域名/scpsl`，保留 Authorization 头和 WebSocket 升级。不要向外分发 `config.json` 与生成的 `plugin-端口.yml`，其中含真实票据；本项目的运行包和源码包均排除这些文件。

`status_image` 可以填写与 `config.json` 同目录下的相对图片路径或绝对路径，最大 8 MiB。官方长回复会按 UTF-8 边界拆分，遵守单个事件的回复次数；剩余次数不足时跳过图片或后续内容，不另起主动消息补发。

命令行选项：`--setup` 重新配置、`--check` 检查后退出、`--headless` 禁用交互等待、`--config 路径` 指定配置。常驻运行时可直接使用 `QQBotLite.exe --headless`。

编译、协议和验证记录见 [开发说明](docs/development.md)、[桥接协议](docs/protocol.md)、[验证记录](docs/validation.md)。

## 许可

本项目采用 [MIT License](LICENSE)。第三方组件保留各自许可，详见 [第三方许可声明](THIRD_PARTY_NOTICES.txt)。
