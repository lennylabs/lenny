import java.net.*; import java.net.http.*;
public class J { public static void main(String[] a) throws Exception {
  var c = HttpClient.newBuilder().proxy(ProxySelector.getDefault()).build();
  var r = c.send(HttpRequest.newBuilder(URI.create("https://api.example.test/acme/check")).header("Authorization","Bearer "+System.getenv("ACME_API_KEY")).build(), HttpResponse.BodyHandlers.ofString());
  System.out.println("java " + r.version() + " " + r.statusCode() + " " + r.body().trim()); } }
