// HttpClient against each case. Mode "default" uses the platform trust (SSL_CERT_FILE, the design's variable). Mode "custom" uses CustomRootTrust.
// A HttpClient with no explicit proxy reads HTTPS_PROXY. The handler's chain status is printed through a callback that defers to the platform result.
using System.Net;
using System.Net.Http;
using System.Net.Security;
using System.Security.Cryptography.X509Certificates;
using System.Text.Json;

var mode = args[0];
var caFiles = args[1].Split(':', StringSplitOptions.RemoveEmptyEntries);
Console.WriteLine($"runtime={System.Runtime.InteropServices.RuntimeInformation.FrameworkDescription} SSL_CERT_FILE={Environment.GetEnvironmentVariable("SSL_CERT_FILE")} SSL_CERT_DIR={Environment.GetEnvironmentVariable("SSL_CERT_DIR")} HTTPS_PROXY={Environment.GetEnvironmentVariable("HTTPS_PROXY")}");
foreach (var url in args.Skip(2))
{
    string chain = "";
    var h = new SocketsHttpHandler();
    if (mode == "observe" || mode == "custom")
    {
        h.SslOptions = new SslClientAuthenticationOptions
        {
            RemoteCertificateValidationCallback = (s, cert, ch, errs) =>
            {
                var st = ch == null ? "" : string.Join(",", ch.ChainStatus.Select(x => x.Status.ToString()));
                chain = $"sslPolicyErrors={errs} chainStatus=[{st}]";
                if (mode == "observe") return errs == SslPolicyErrors.None;
                using var c2 = new X509Chain();
                c2.ChainPolicy.TrustMode = X509ChainTrustMode.CustomRootTrust;
                c2.ChainPolicy.RevocationMode = X509RevocationMode.NoCheck;
                foreach (var f in caFiles) c2.ChainPolicy.CustomTrustStore.Add(X509Certificate2.CreateFromPem(File.ReadAllText(f)));
                if (ch != null) foreach (var e in ch.ChainElements) c2.ChainPolicy.ExtraStore.Add(e.Certificate);
                var ok = c2.Build(new X509Certificate2(cert!));
                chain = $"customRoot ok={ok} chainStatus=[{string.Join(",", c2.ChainStatus.Select(x => x.Status.ToString()))}] sslPolicyErrors={errs}";
                return ok;
            }
        };
    }
    using var cl = new HttpClient(h) { Timeout = TimeSpan.FromSeconds(10) };
    var line = new Dictionary<string, object?> { ["url"] = url };
    try
    {
        var r = await cl.GetAsync(url);
        var b = await r.Content.ReadAsStringAsync();
        line["result"] = $"HTTP {(int)r.StatusCode} {b[..Math.Min(b.Length, 60)]}";
    }
    catch (Exception e)
    {
        var m = e.Message; var i = e.InnerException;
        while (i != null) { m += " | " + i.Message; i = i.InnerException; }
        line["result"] = "REFUSED"; line["error"] = m.Length > 260 ? m[..260] : m;
    }
    line["chain"] = chain;
    Console.WriteLine(JsonSerializer.Serialize(line));
}
