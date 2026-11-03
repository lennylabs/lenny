package spike;

import java.time.*;
import software.amazon.awssdk.regions.Region;
import software.amazon.awssdk.services.s3.S3Client;
import software.amazon.awssdk.core.util.VersionInfo;

public class Main {
  public static void main(String[] a) throws Exception {
    System.out.println("aws-sdk-java-v2 " + VersionInfo.SDK_VERSION + " java " + System.getProperty("java.version"));
    S3Client s3 = S3Client.builder().region(Region.US_EAST_1).forcePathStyle(true).build();
    int dur = Integer.parseInt(System.getenv().getOrDefault("LOOP_SECONDS", "0"));
    int iv = Integer.parseInt(System.getenv().getOrDefault("INTERVAL", "15"));
    long start = System.currentTimeMillis();
    while (true) {
      String t = LocalTime.now(ZoneOffset.UTC).withNano(0).toString();
      try { var r = s3.listBuckets(); System.out.println(t + " OK " + r.buckets().get(0).name()); }
      catch (Exception e) { System.out.println(t + " ERR " + e.getClass().getSimpleName() + " " + String.valueOf(e.getMessage()).substring(0, Math.min(300, String.valueOf(e.getMessage()).length()))); }
      if ((System.currentTimeMillis() - start) / 1000 + iv > dur) break;
      Thread.sleep(iv * 1000L);
    }
  }
}
