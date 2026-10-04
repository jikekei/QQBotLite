# 桥接协议 v1

游戏插件主动连接机器人 `ws://127.0.0.1:17890/scpsl?server_id=31140`，握手使用 `Authorization: Bearer 服务器Token`。每个服务器独立 Token；错误鉴权返回 401，同 ID 同时连接返回 409。公网部署可由 HTTPS 反代提供 WSS。

采用 [JSON-RPC 2.0](https://www.jsonrpc.org/specification)，一条 WebSocket 文本帧携带一个对象，不支持批处理；双方请求 ID 为字符串。只有机器人向插件发送带 ID 的操作请求，插件向机器人发送通知。

## 插件通知

连接完成后先发送：

```json
{"jsonrpc":"2.0","method":"server.hello","params":{"version":1}}
```

每 15 秒发送 `server.heartbeat`。握手版本为 1 才标记服务器可用。`.ac` 发送通知：

```json
{"jsonrpc":"2.0","method":"server.help","params":{"event_id":"随机唯一ID","player":"玩家昵称","text":"求助内容"}}
```

求助通知没有持久化和回放；通知发送失败会丢弃。机器人用服务器 ID + 事件 ID 去重。

## 机器人操作

```json
{"jsonrpc":"2.0","id":"唯一请求ID","method":"kick","params":{"player_id":17}}
```

| method | params | 行为 |
| --- | --- | --- |
| `cx` | `{}` | 人数、容量、在线管理员 |
| `info` | `{}` | 上述状态及角色、回合与刷新信息 |
| `list` | `{}` | 玩家昵称与游戏内 ID |
| `start` | `{}` | 开启回合 |
| `rest` | `{}` | 回合重启，先回应已受理 |
| `allrest` | `{}` | 进程重启，先回应已受理 |
| `kick` | `{"player_id":17}` | 踢人，不封禁 |
| `bc` | `{"text":"公告"}` | 10 秒广播 |

响应示例：

```json
{"jsonrpc":"2.0","id":"唯一请求ID","result":{"message":"已踢出玩家","online":0,"max":0,"admins":0,"class_d":0,"scientists":0,"scps":0,"round_seconds":0,"round_count":0}}
```

查询返回 `name`、`online`、`max`、`admins`；`info` 另含 `class_d`、`scientists`、`scps`、`round_seconds`、`round_count` 与可空的 `respawn_seconds`。`list` 包含 `players: [{"name":"昵称","id":17}]`。其他操作结果放 `message`。

错误包含 `error.code` 和 `error.message`。未知操作为 -32601；执行失败 -32000；队列请求过期 -32002。机器人每个服等待 5 秒；插件进入队列后超过 4 秒未执行就丢弃，队列上限 128。操作在 Unity 主线程执行，单次 Pump 最多 16 个请求。

插件保留最近 256 个请求结果，重复请求 ID 返回已存结果，避免同一操作重复执行。断开的旧连接请求不执行；管理超时不会重发。重启操作先成功写出“已受理”响应，延迟 500 毫秒后回到主线程调用游戏重启 API。已经受理的重启不会因为之后 QQ 发送失败而撤销。
