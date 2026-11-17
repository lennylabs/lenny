import software.amazon.awssdk.auth.credentials.ContainerCredentialsProvider;
public class X { public static void main(String[] a) {
  try { System.out.println(ContainerCredentialsProvider.builder().build().resolveCredentials()); }
  catch (Exception e) { System.out.println(e.getClass().getSimpleName() + ": " + e.getMessage()); } } }
