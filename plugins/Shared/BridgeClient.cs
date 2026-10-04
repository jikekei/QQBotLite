using System;
using System.Collections.Concurrent;
using System.Collections.Generic;
using System.IO;
using System.Net.WebSockets;
using System.Threading;
using System.Threading.Tasks;

namespace QQBotLite.Shared
{
    // Network threads never access game wrappers. Pump() runs only on the Unity main thread.
    public sealed class BridgeClient : IDisposable
    {
        private readonly BridgeConfig config;
        private readonly IGame game;
        private readonly Action<string> log;
        private readonly ConcurrentQueue<Action> mainQueue=new ConcurrentQueue<Action>();
        private readonly CancellationTokenSource lifetime=new CancellationTokenSource();
        private readonly SemaphoreSlim sendLock=new SemaphoreSlim(1,1);
        private readonly Dictionary<string,RpcPacket> responses=new Dictionary<string,RpcPacket>();
        private readonly Queue<string> responseOrder=new Queue<string>();
        private readonly Dictionary<string,DateTime> helpCooldown=new Dictionary<string,DateTime>();
        private ClientWebSocket socket;
        private Task loop;
        private int queued;
        private int disposed;

        public BridgeClient(BridgeConfig config,IGame game,Action<string> log){this.config=config;this.game=game;this.log=log;}
        public bool Start()
        {
            Uri uri;
            if (string.IsNullOrWhiteSpace(config.ServerId)||config.Token==null||config.Token.Length<32||!Uri.TryCreate(config.BotUrl,UriKind.Absolute,out uri)||(uri.Scheme!="ws"&&uri.Scheme!="wss"))
            {log("配置无效：请填写 bot_url、server_id 和机器人生成的 Token。未启动连接。");return false;}
            loop=Task.Run(()=>ConnectLoop(lifetime.Token));return true;
        }
        public void Pump()
        {
            if (Volatile.Read(ref disposed)!=0)return;
            for (int i=0;i<16;i++) {Action action;if (!mainQueue.TryDequeue(out action))break;Interlocked.Decrement(ref queued);try{action();}catch(Exception ex){log("主线程处理错误："+ex.GetType().Name);}}
        }
        private async Task ConnectLoop(CancellationToken token)
        {
            var delay=1;
            while (!token.IsCancellationRequested)
            {
                using(var ws=new ClientWebSocket())
                {
                    try
                    {
                        ws.Options.SetRequestHeader("Authorization","Bearer "+config.Token);
                        ws.Options.KeepAliveInterval=TimeSpan.FromSeconds(15);
                        var separator=config.BotUrl.Contains("?")?"&":"?";
                        using(var connect=CancellationTokenSource.CreateLinkedTokenSource(token))
                        {connect.CancelAfter(TimeSpan.FromSeconds(10));await ws.ConnectAsync(new Uri(config.BotUrl+separator+"server_id="+Uri.EscapeDataString(config.ServerId)),connect.Token).ConfigureAwait(false);}
                        socket=ws;delay=1;
                        await Send(ws,new RpcPacket{Method="server.hello",Params=new RequestParams{Version=1}},token).ConfigureAwait(false);
                        log("游戏插件已连接机器人，服务器 ID="+config.ServerId);
                        using(var session=CancellationTokenSource.CreateLinkedTokenSource(token))
                        {
                            var heartbeat=Heartbeat(ws,session.Token);
                            try {await Receive(ws,session.Token).ConfigureAwait(false);}
                            finally {session.Cancel();try{await heartbeat.ConfigureAwait(false);}catch(OperationCanceledException){} }
                        }
                    }
                    catch(OperationCanceledException) when(token.IsCancellationRequested){break;}
                    catch(Exception ex){if(!token.IsCancellationRequested)log("连接中断/失败（"+ex.GetType().Name+"），将自动重连；核对地址与 Token。 ");}
                    finally {if (ReferenceEquals(socket,ws))socket=null;ws.Abort();}
                }
                try {await Task.Delay(TimeSpan.FromSeconds(delay),token).ConfigureAwait(false);}catch(OperationCanceledException){break;}
                delay=Math.Min(delay*2,30);
            }
        }
        private async Task Heartbeat(ClientWebSocket ws,CancellationToken token)
        {
            while (!token.IsCancellationRequested)
            {
                await Task.Delay(TimeSpan.FromSeconds(15),token).ConfigureAwait(false);
                try {await Send(ws,new RpcPacket{Method="server.heartbeat"},token).ConfigureAwait(false);}
                catch {ws.Abort();throw;}
            }
        }
        private async Task Receive(ClientWebSocket ws,CancellationToken token)
        {
            var buffer=new byte[8192];
            while (ws.State==WebSocketState.Open&&!token.IsCancellationRequested)
            {
                using(var stream=new MemoryStream())
                {
                    WebSocketReceiveResult part;
                    do
                    {
                        part=await ws.ReceiveAsync(new ArraySegment<byte>(buffer),token).ConfigureAwait(false);
                        if (part.MessageType==WebSocketMessageType.Close)return;
                        if (part.MessageType!=WebSocketMessageType.Text||stream.Length+part.Count>65536)throw new InvalidDataException("RPC 过大或不是文本");
                        stream.Write(buffer,0,part.Count);
                    }while(!part.EndOfMessage);
                    var request=Json.Decode(stream.ToArray());
                    if (request.JsonRpc!="2.0"||string.IsNullOrEmpty(request.Id)||string.IsNullOrEmpty(request.Method))continue;
                    if (Interlocked.Increment(ref queued)>128) {Interlocked.Decrement(ref queued);await Send(ws,Failure(request.Id,-32000,"游戏服请求队列已满"),token).ConfigureAwait(false);continue;}
                    var deadline=DateTime.UtcNow.AddSeconds(4);
                    mainQueue.Enqueue(()=>Dispatch(ws,request,deadline));
                }
            }
        }
        private static RpcPacket Failure(string id,int code,string message){return new RpcPacket{Id=id,Error=new RpcError{Code=code,Message=message}};}
        private void Dispatch(ClientWebSocket ws,RpcPacket request,DateTime deadline)
        {
            // A request belonging to a disconnected socket must never execute after a reconnect.
            if (lifetime.IsCancellationRequested||ws.State!=WebSocketState.Open)return;
            RpcPacket response;
            if(responses.TryGetValue(request.Id,out response)){_ = SafeSend(ws,response);return;}
            if(DateTime.UtcNow>deadline){_ = SafeSend(ws,Failure(request.Id,-32002,"请求已过期，未执行"));return;}
            Action afterResponse=null;
            try
            {
                var args=request.Params??new RequestParams();GameResult result;
                switch(request.Method)
                {
                    case "cx":case "info":case "list":result=game.Query(request.Method);break;
                    case "start":result=new GameResult{Message=game.Start()};break;
                    case "rest":result=new GameResult{Message="已受理重启回合"};afterResponse=game.RestartRound;break;
                    case "allrest":result=new GameResult{Message="已受理重启服务器"};afterResponse=game.RestartServer;break;
                    case "kick":if(args.PlayerId<1)throw new ArgumentException("玩家 ID 必须大于 0");result=new GameResult{Message=game.Kick(args.PlayerId)};break;
                    case "bc":if(string.IsNullOrWhiteSpace(args.Text)||args.Text.Length>1000)throw new ArgumentException("广播长度无效");game.Broadcast(args.Text);result=new GameResult{Message="广播发送成功"};break;
                    default:response=Failure(request.Id,-32601,"未知操作");Remember(response);_ = SafeSend(ws,response);return;
                }
                response=new RpcPacket{Id=request.Id,Result=result};
            }
            catch(Exception ex){response=Failure(request.Id,-32000,ex is ArgumentException?ex.Message:"游戏操作失败，请检查服务端日志");log("操作失败："+request.Method+" / "+ex.GetType().Name);}
            Remember(response);
            if(afterResponse==null){_ = SafeSend(ws,response);return;}
            _ = AcknowledgeThenSchedule(ws,response,afterResponse);
        }
        private void Remember(RpcPacket response)
        {
            responses[response.Id]=response;responseOrder.Enqueue(response.Id);
            while(responseOrder.Count>256)responses.Remove(responseOrder.Dequeue());
        }
        private async Task AcknowledgeThenSchedule(ClientWebSocket ws,RpcPacket response,Action action)
        {
            if (!await SafeSend(ws,response).ConfigureAwait(false))return;
            try {await Task.Delay(500,lifetime.Token).ConfigureAwait(false);}catch(OperationCanceledException){return;}
            if(lifetime.IsCancellationRequested)return;
            // Return to the game's main thread for both restart APIs.
            Interlocked.Increment(ref queued);mainQueue.Enqueue(action);
        }
        private async Task<bool> SafeSend(ClientWebSocket ws,RpcPacket packet)
        {
            try {await Send(ws,packet,lifetime.Token).ConfigureAwait(false);return true;}
            catch(Exception){if(!lifetime.IsCancellationRequested)log("RPC 回复未送达；不自动重复执行管理操作。");return false;}
        }
        private async Task Send(ClientWebSocket ws,RpcPacket packet,CancellationToken token)
        {
            using(var timeout=CancellationTokenSource.CreateLinkedTokenSource(token))
            {
                timeout.CancelAfter(TimeSpan.FromSeconds(5));await sendLock.WaitAsync(timeout.Token).ConfigureAwait(false);
                try {var bytes=Json.Encode(packet);await ws.SendAsync(new ArraySegment<byte>(bytes),WebSocketMessageType.Text,true,timeout.Token).ConfigureAwait(false);}
                finally {sendLock.Release();}
            }
        }
        public string Help(string playerId,string nickname,string text)
        {
            if(!config.EnableHelp)return "管理求助已关闭。";
            text=(text??"").Trim();if(text.Length==0||text.Length>500)return "用法：.ac 求助内容（最多 500 字）";
            DateTime last;if(helpCooldown.TryGetValue(playerId,out last)&&(DateTime.UtcNow-last).TotalSeconds<30)return "求助发送过于频繁，请稍后再试。";
            helpCooldown[playerId]=DateTime.UtcNow;
            game.NotifyAdmins(nickname,text);
            var ws=socket;
            if(ws==null||ws.State!=WebSocketState.Open)return "已提示在线管理，机器人当前未连接，QQ 通知未发送。";
            var packet=new RpcPacket{Method="server.help",Params=new RequestParams{EventId=Guid.NewGuid().ToString("N"),Player=nickname,Text=text}};
            _ = SafeSend(ws,packet);
            return "已提示在线管理并提交 QQ 通知；未配置目标或平台不允许发送时会跳过，不缓存或补发。";
        }
        public void Dispose()
        {
            if(Interlocked.Exchange(ref disposed,1)!=0)return;lifetime.Cancel();socket?.Abort();
            Action action;while(mainQueue.TryDequeue(out action)){}Interlocked.Exchange(ref queued,0);
            // The connection task owns socket/CTS disposal; never block the Unity thread waiting on networking.
        }
    }
}
