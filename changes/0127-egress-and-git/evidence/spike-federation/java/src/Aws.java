import software.amazon.awssdk.services.dynamodb.DynamoDbClient;
import java.nio.file.*;
public class Aws {
  static void swap(int v) throws Exception {
    String d = "/var/run/netagent/aws";
    Files.copy(Path.of(d + "/token.v" + v), Path.of(d + "/token.tmp"), StandardCopyOption.REPLACE_EXISTING);
    Files.move(Path.of(d + "/token.tmp"), Path.of(d + "/token"), StandardCopyOption.ATOMIC_MOVE, StandardCopyOption.REPLACE_EXISTING);
  }
  public static void main(String[] a) throws Exception {
    swap(1);
    DynamoDbClient c = DynamoDbClient.create();
    System.out.println("call1 -> " + c.listTables().tableNames());
    swap(2);
    Thread.sleep(1000L * Integer.parseInt(System.getenv().getOrDefault("SLEEP", "31")));
    System.out.println("call2 -> " + c.listTables().tableNames());
  }
}
