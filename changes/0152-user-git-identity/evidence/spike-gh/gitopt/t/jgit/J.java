import org.eclipse.jgit.api.Git;
import java.io.File;
public class J {
  public static void main(String[] a) throws Exception {
    try (Git g = Git.cloneRepository().setURI(a[0]).setDirectory(new File(a[1])).call()) {
      System.out.println("cloned " + g.getRepository().resolve("HEAD").getName().substring(0, 12));
    }
  }
}
