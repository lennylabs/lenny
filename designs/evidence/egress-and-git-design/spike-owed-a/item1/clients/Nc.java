import java.net.URI; import java.net.http.*; import java.time.Duration;
public class Nc { public static void main(String[] a) throws Exception {
  System.err.println("java "+System.getProperty("java.version")+" "+System.getProperty("java.vendor"));
  for (String u: a) { try { HttpClient c=HttpClient.newBuilder().connectTimeout(Duration.ofSeconds(8)).build();
    HttpResponse<String> r=c.send(HttpRequest.newBuilder(URI.create(u)).timeout(Duration.ofSeconds(8)).build(),HttpResponse.BodyHandlers.ofString());
    System.out.println("java-httpclient "+u+" OK "+r.statusCode()+" "+r.version());
  } catch(Exception e){ Throwable t=e; while(t.getCause()!=null) t=t.getCause(); String m=(t.getClass().getSimpleName()+": "+t.getMessage()); System.out.println("java-httpclient "+u+" REFUSED "+m.substring(0,Math.min(150,m.length()))); } }
  }}
