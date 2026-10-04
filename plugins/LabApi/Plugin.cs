using System;
using System.Collections.Generic;
using CommandSystem;
using LabApi.Features;
using LabApi.Features.Console;
using LabApi.Features.Wrappers;
using LabApi.Loader.Features.Plugins;
using MEC;
using QQBotLite.Shared;

namespace QQBotLite.LabApi
{
    public sealed class Config : BridgeConfig { }
    public sealed class Main : Plugin<Config>
    {
        public override string Name=>"QQBotLite.LabApi";
        public override string Description=>"轻量 QQ 机器人查询、管理及求助桥接插件";
        public override string Author=>"幻梦银河";
        public override Version Version=>new Version(1,0,0);
        public override Version RequiredApiVersion=>new Version(LabApiProperties.CompiledVersion);
        internal static BridgeClient Client;
        private CoroutineHandle pump;
        public override void Enable()
        {
            if(!Config.IsEnabled)return;
            Client=new BridgeClient(Config,new GameAdapter(Config),s=>Logger.Info("[QQBotLite] "+s));
            if(!Client.Start()){Client.Dispose();Client=null;return;}
            pump=Timing.RunCoroutine(Pump(),Segment.Update);
        }
        private IEnumerator<float> Pump(){while(Client!=null){Client.Pump();yield return Timing.WaitForSeconds(0.05f);}}
        public override void Disable(){Timing.KillCoroutines(pump);Client?.Dispose();Client=null;}
    }
    [CommandHandler(typeof(ClientCommandHandler))]
    [CommandHandler(typeof(GameConsoleCommandHandler))]
    public sealed class HelpCommand : ICommand
    {
        public string Command=>"ac";
        public string[] Aliases=>Array.Empty<string>();
        public string Description=>"发送管理求助，用法：.ac 内容";
        public bool Execute(ArraySegment<string> arguments,ICommandSender sender,out string response)
        {
            var player=Player.Get(sender);
            if(player==null){response="未找到您的玩家，请重新登录后再试。";return false;}
            if(Main.Client==null){response="QQBotLite 插件尚未启用，请联系服主检查配置。";return false;}
            response=Main.Client.Help(player.UserId,player.Nickname,string.Join(" ",arguments));return true;
        }
    }
}
