using System;
using System.Threading;
using QQBotLite.Shared;

// Runs the exact production plugin network/main-thread code against the Go bridge.
// No game server or production QQ credentials are needed.
internal static class Program
{
    private sealed class FakeGame : IGame
    {
        private readonly int mainThread=Environment.CurrentManagedThreadId;
        public int Queries,Kicks,Restarts,ServerRestarts,Broadcasts,Notifications;
        private void Check(){if(Environment.CurrentManagedThreadId!=mainThread)throw new InvalidOperationException("game access off main thread");}
        public GameResult Query(string method){Check();Queries++;return new GameResult{Name="模拟服",Online=3,Max=25,Admins=1,ClassD=1,Scientists=1,Scps=1,RoundSeconds=123,RoundCount=4,RespawnSeconds=20,Players=new[]{new PlayerEntry{Name="测试玩家",Id=7}},Message=$"kicks={Kicks};rest={Restarts};allrest={ServerRestarts};bc={Broadcasts};notifications={Notifications}"};}
        public string Start(){Check();return "started";}
        public void RestartRound(){Check();Restarts++;}
        public void RestartServer(){Check();ServerRestarts++;}
        public string Kick(int playerId){Check();Kicks++;return $"kick:{playerId}:{Kicks}";}
        public void Broadcast(string text){Check();Broadcasts++;}
        public void NotifyAdmins(string player,string text){Check();Notifications++;}
    }
    public static int Main(string[] args)
    {
        if(args.Length!=3)return 2;
        var game=new FakeGame();
        var config=new BridgeConfig{BotUrl=args[0],Token=args[1],ServerId=args[2]};
        using(var client=new BridgeClient(config,game,Console.WriteLine))
        {
            if(!client.Start())return 3;
            var end=DateTime.UtcNow.AddSeconds(15);var helped=false;
            while(DateTime.UtcNow<end)
            {
                client.Pump();
                if(game.Queries>0&&!helped)
                {
                    helped=true;client.Help("u","测试玩家","需要管理");
                    if(!client.Help("u","测试玩家","重复求助").Contains("频繁"))return 4;
                }
                Thread.Sleep(10);
            }
        }
        return 0;
    }
}
