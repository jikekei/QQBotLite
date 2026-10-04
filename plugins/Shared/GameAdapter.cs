using System;
using System.Linq;
using System.Text.RegularExpressions;
using LabApi.Features.Wrappers;
using PlayerRoles;
using RoundRestarting;

namespace QQBotLite.Shared
{
    // EXILED 9.14.2 is built on LabAPI; both loaders use the same current game wrappers.
    public sealed class GameAdapter : IGame
    {
        private readonly BridgeConfig config;
        public GameAdapter(BridgeConfig config){this.config=config;}
        private static Player[] Players()=>Player.List.Where(p=>!p.IsHost&&!p.IsDummy).ToArray();
        public GameResult Query(string method)
        {
            var players=Players();
            var result=new GameResult{Name=config.ServerName,Online=players.Length,Max=Server.MaxPlayers,Admins=players.Count(p=>p.RemoteAdminAccess)};
            if(method=="list")result.Players=players.Select(p=>new PlayerEntry{Name=Plain(p.Nickname),Id=p.PlayerId}).ToArray();
            if(method=="info")
            {
                result.ClassD=players.Count(p=>p.Role==RoleTypeId.ClassD);
                result.Scientists=players.Count(p=>p.Role==RoleTypeId.Scientist);
                result.Scps=players.Count(p=>p.Team==Team.SCPs);
                result.RoundSeconds=(int)Round.Duration.TotalSeconds;
                result.RoundCount=RoundRestart.UptimeRounds;
                // The old plugin used spawn protection duration as a respawn timer. Use actual wave timers.
                RespawnWave[] waves={RespawnWaves.PrimaryMtfWave,RespawnWaves.PrimaryChaosWave};
                var times=waves.Where(w=>w!=null&&!w.IsForcefullyPaused).Select(w=>(int)Math.Ceiling(w.TimeLeft)).Where(t=>t>=0).ToArray();
                if(times.Length>0&&Round.IsRoundStarted)result.RespawnSeconds=times.Min();
            }
            return result;
        }
        public string Start(){if(Round.IsRoundStarted)return "回合已经开启了";Round.Start();return "回合启动成功";}
        public void RestartRound()=>Round.Restart(true,false,ServerStatic.NextRoundAction.DoNothing);
        public void RestartServer()=>Server.Restart();
        public string Kick(int playerId)
        {
            var player=Players().FirstOrDefault(p=>p.PlayerId==playerId);
            if(player==null)return "踢出失败，未找到指定 ID 的玩家";
            var name=Plain(player.Nickname);player.Kick("QQ 管理员执行踢出");return "已踢出玩家："+name+"（没有封禁）";
        }
        public void Broadcast(string text)=>Server.SendBroadcast("[管理员消息]"+Escape(text),10,global::Broadcast.BroadcastFlags.Normal,false);
        public void NotifyAdmins(string nickname,string text)
        {
            foreach(var admin in Players().Where(p=>p.RemoteAdminAccess))
            {
                admin.SendConsoleMessage("[管理员求助]"+Plain(nickname)+":"+Plain(text),"white");
                admin.SendHint("<color=#01fdfd>[管理员求助]</color>"+Escape(nickname)+":"+Escape(text),10f);
            }
        }
        private static string Plain(string text)=>Regex.Replace(text??"","<[^>]*>","");
        private static string Escape(string text)=>(text??"").Replace("<","＜").Replace(">","＞");
    }
}
