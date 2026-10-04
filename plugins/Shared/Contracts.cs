using System;
using System.ComponentModel;
using System.IO;
using System.Runtime.Serialization;
using System.Runtime.Serialization.Json;

namespace QQBotLite.Shared
{
    public class BridgeConfig
    {
        [Description("启用插件")]
        public bool IsEnabled { get; set; } = true;
        [Description("调试日志")]
        public bool Debug { get; set; } = false;
        [Description("机器人连接地址，同机保持默认；远程部署建议使用 wss")]
        public string BotUrl { get; set; } = "ws://127.0.0.1:17890/scpsl";
        [Description("必须与机器人服务器条目的 id 一致，例如游戏端口 31140")]
        public string ServerId { get; set; } = "31140";
        [Description("服务器名称")]
        public string ServerName { get; set; } = "1服";
        [Description("填写机器人首次引导生成的独立随机 Token，不可为空")]
        public string Token { get; set; } = "";
        [Description("启用游戏内 .ac 管理求助")]
        public bool EnableHelp { get; set; } = true;
    }
    [DataContract]
    public class RequestParams
    {
        [DataMember(Name="player_id",EmitDefaultValue=false)] public int PlayerId;
        [DataMember(Name="text",EmitDefaultValue=false)] public string Text;
        [DataMember(Name="version",EmitDefaultValue=false)] public int Version;
        [DataMember(Name="event_id",EmitDefaultValue=false)] public string EventId;
        [DataMember(Name="player",EmitDefaultValue=false)] public string Player;
    }
    [DataContract]
    public class PlayerEntry
    {
        [DataMember(Name="name")] public string Name;
        [DataMember(Name="id")] public int Id;
    }
    [DataContract]
    public class GameResult
    {
        [DataMember(Name="name",EmitDefaultValue=false)] public string Name;
        [DataMember(Name="online")] public int Online;
        [DataMember(Name="max")] public int Max;
        [DataMember(Name="admins")] public int Admins;
        [DataMember(Name="class_d")] public int ClassD;
        [DataMember(Name="scientists")] public int Scientists;
        [DataMember(Name="scps")] public int Scps;
        [DataMember(Name="round_seconds")] public int RoundSeconds;
        [DataMember(Name="round_count")] public int RoundCount;
        [DataMember(Name="respawn_seconds",EmitDefaultValue=false)] public int? RespawnSeconds;
        [DataMember(Name="players",EmitDefaultValue=false)] public PlayerEntry[] Players;
        [DataMember(Name="message",EmitDefaultValue=false)] public string Message;
    }
    [DataContract]
    public class RpcError
    {
        [DataMember(Name="code")] public int Code;
        [DataMember(Name="message")] public string Message;
    }
    [DataContract]
    public class RpcPacket
    {
        [DataMember(Name="jsonrpc")] public string JsonRpc = "2.0";
        [DataMember(Name="id",EmitDefaultValue=false)] public string Id;
        [DataMember(Name="method",EmitDefaultValue=false)] public string Method;
        [DataMember(Name="params",EmitDefaultValue=false)] public RequestParams Params;
        [DataMember(Name="result",EmitDefaultValue=false)] public GameResult Result;
        [DataMember(Name="error",EmitDefaultValue=false)] public RpcError Error;
    }
    public static class Json
    {
        public static byte[] Encode(RpcPacket packet)
        {
            using (var stream=new MemoryStream()) {new DataContractJsonSerializer(typeof(RpcPacket)).WriteObject(stream,packet);return stream.ToArray();}
        }
        public static RpcPacket Decode(byte[] bytes)
        {
            using (var stream=new MemoryStream(bytes)) return (RpcPacket)new DataContractJsonSerializer(typeof(RpcPacket)).ReadObject(stream);
        }
    }
    public interface IGame
    {
        GameResult Query(string method);
        string Start();
        void RestartRound();
        void RestartServer();
        string Kick(int playerId);
        void Broadcast(string text);
        void NotifyAdmins(string player,string text);
    }
}
