import java.net.URI;
import java.net.http.*;
public class Fetch {
  public static void main(String[] a) throws Exception {
    var c = HttpClient.newHttpClient();
    var r = c.send(HttpRequest.newBuilder(URI.create(a[0])).build(), HttpResponse.BodyHandlers.discarding());
    System.out.println(r.statusCode());
    var u = (java.net.HttpURLConnection) URI.create(a[0].replace("javahttp", "javahuc")).toURL().openConnection();
    System.out.println("HttpURLConnection " + u.getResponseCode());
  }
}
