using System;
using System.Collections.Generic;
using System.IO;
using System.Runtime.Serialization;
using System.Runtime.Serialization.Json;
using System.Text;

// Native test-only Link CLI. The desktop app still spawns one fixed executable with its original argv and stdin.
public sealed class FakePersonaLink
{
    [DataContract]
    public sealed class Command
    {
        [DataMember] public string[] argv;
        [DataMember] public string stdin;
    }

    private static void Record(string log, string[] args, string input)
    {
        var command = new Command { argv = args, stdin = input };
        var serializer = new DataContractJsonSerializer(typeof(Command));
        using (var stream = new MemoryStream())
        {
            serializer.WriteObject(stream, command);
            File.AppendAllText(log, Encoding.UTF8.GetString(stream.ToArray()) + "\n", new UTF8Encoding(false));
        }
    }

    public static int Main(string[] args)
    {
        string input = Console.In.ReadToEnd();
        Record(Environment.GetEnvironmentVariable("PERSONA_TEST_LINK_LOG"), args, input);
        if (args.Length == 2 && args[0] == "status" && args[1] == "--json")
        {
            Console.Out.Write("{\"running\":true,\"machine\":\"Example computer\",\"link\":{\"state\":\"connected\"},\"token\":\"must-not-reach-the-renderer\",\"config\":\"/must-not-appear\"}\n");
            return 0;
        }
        if (args.Length == 2 && args[0] == "pair" && args[1] == "-")
        {
            Console.Out.Write("Paired the example computer\n");
            return 0;
        }
        return 2;
    }
}
