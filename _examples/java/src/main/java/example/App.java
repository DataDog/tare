package example;

import io.netty.util.Version;
import java.util.Map;

public class App {
    public static void main(String[] args) {
        Map<String, Version> versions = Version.identify();
        for (Map.Entry<String, Version> entry : versions.entrySet()) {
            System.out.println(entry.getKey() + ": " + entry.getValue());
        }
        System.out.println("hello from java");
    }
}
