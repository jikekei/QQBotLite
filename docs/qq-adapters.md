# QQ 接入说明

## NapCat

先运行 NapCat 并登录 QQ，在 NapCat WebUI 的网络配置中添加 **WebSocket 服务端**，启用后设置主机 `127.0.0.1`、端口 `3001`。使用正向 WebSocket，机器人作为客户端连接；消息上报可用消息段数组或字符串。开启 Token 时，在机器人引导中填写相同 Token。跨机器部署填写实际地址。[NapCat WebUI 文档](https://napneko.github.io/config/basic)

机器人示例配置：

```json
"adapter": "napcat",
"napcat": {"url": "ws://127.0.0.1:3001", "token": "NapCat中设置的Token"}
```

在引导中填数字 QQ 号和群号。无需设置 QQ 开放平台、Webhook、公网域名或数据库。NapCat Token 鉴权失败会明确停止；其他连接故障自动重连。重连只恢复连接，不补发之前的回复或求助。

## QQ 官方 API

在 QQ 开放平台创建并配置机器人，获取 AppID 与 AppSecret，在平台启用所需群 / C2C 能力、完成测试成员及群设置、配置平台要求的服务器 IP 白名单，再在本项目引导中选择官方 API。只支持实际开通的能力，不会绕过平台权限。

官方 ID 是 OpenID。先在测试群 @ 机器人发 `/cx`，从机器人日志复制会话 ID / 用户 ID，填写允许群、管理员、求助目标。AppID、数字 QQ 号和群 OpenID 相互不同。只有平台提供 `member_role` 时才能直接判断当前群管理员与群主；缺少身份时填写明确管理员 OpenID。[群事件字段](https://bot.q.qq.com/wiki/develop/api-v2/autogen/event/group_at_message_create.html)

### WebSocket 接收

引导中选择 WebSocket，机器人自动请求访问票据和网关、鉴权、心跳、恢复会话。使用账号实际支持的事件接收方式；平台未提供网关或拒绝权限时，按平台能力改用 Webhook。[事件接收框架](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/event-emit.html)

### Webhook 接收

引导中选择 Webhook，默认本地监听 `127.0.0.1:8080`，路径 `/qq/webhook`。准备公网 HTTPS 地址，将请求反代到此监听，在平台配置完整 HTTPS 回调 URL，并保留 `X-Bot-Appid`、`X-Signature-Timestamp`、`X-Signature-Ed25519` 头。

机器人自动响应平台验证挑战；事件使用 AppSecret 派生的 Ed25519 密钥验签，验证失败返回错误，不执行指令。启用时校验时间戳，服务器时间应正常同步。[官方签名说明](https://bot.q.qq.com/wiki/develop/api-v2/dev-prepare/interface-framework/sign.html)

### 回复与求助

查询和管理结果使用当前事件 ID 被动回复；游戏求助属于主动消息，只有账号具备对应能力、目标会话允许时才发送。主动消息不借用之前的查询事件。平台拒绝时记录失败，立即丢弃这次通知；不会缓存、不重试、不换会话、不夹带到未来的查询回复。Token 过期时只为之后的新请求刷新票据，不重复发送已失败的请求。

官方群被动回复最多 5 次、C2C 最多 4 次；长文本按字符边界拆分，超出次数会截断。文字用完次数时不再上传状态图片。实际有效窗口、主动额度、授权和发送限制以账号后台和当前官方文档为准。[群消息接口](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_groups_group_openid_messages.post.html)、[消息接口概览](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)

## 常见问题

| 日志或现象 | 处理 |
| --- | --- |
| NapCat 连接失败 | 检查是否登录、WebSocket **服务端**是否启用、地址和端口是否一致 |
| NapCat 鉴权失败 | 检查两个配置的 Token；修改后重启机器人 |
| 官方票据或网关权限被拒绝 | 检查 AppID / AppSecret、平台 IP 白名单、事件接收能力 |
| Webhook 已监听但没有消息 | 平台仍需验证 HTTPS 回调、启用事件并配置测试会话 |
| 官方 API 403 或业务错误码 | 检查平台权限、会话授权、群设置和频率限制；本次消息已丢弃 |
| 服务器未连接 | 检查 DLL 是否加载、插件 Token 与服务器 ID、机器人监听地址 |
| 在线人数为 0 | 已连接的空服，连接故障会明确显示“服务器未连接” |
| 无管理权限 | 检查管理员列表；官方填 OpenID。列表留空只认平台提供的当前群身份 |
| 配置解析失败 | 修复 JSON 格式或字段拼写；程序会保留原文件 |
