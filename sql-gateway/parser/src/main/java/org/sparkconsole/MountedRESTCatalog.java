package org.sparkconsole;

import java.io.IOException;
import java.nio.file.Files;
import java.nio.file.Path;
import java.util.HashMap;
import java.util.Map;
import org.apache.iceberg.rest.RESTCatalog;

/** Loads a dedicated user's Polaris client credential from a Driver Secret volume. */
public final class MountedRESTCatalog extends RESTCatalog {
    @Override
    public void initialize(String name, Map<String, String> props) {
        if (props.containsKey("credential") || props.containsKey("token")) {
            throw new IllegalArgumentException("Inline Polaris credentials are forbidden.");
        }
        var config = new HashMap<>(props);
        String file = config.remove("console-credential-file");
        String principal = config.remove("console-principal-id");
        if (file == null || !file.startsWith("/var/run/spark-console/polaris/")) {
            throw new IllegalArgumentException("A mounted Polaris principal is required.");
        }
        try {
            String credential = Files.readString(Path.of(file)).strip();
            int separator = credential.indexOf(':');
            if (separator < 1 || separator == credential.length() - 1 ||
                    !credential.substring(0, separator).equals(principal) ||
                    credential.substring(0, separator).toLowerCase(java.util.Locale.ROOT).contains("root")) {
                throw new IllegalArgumentException("A dedicated Polaris principal is required.");
            }
            config.put("credential", credential);
        } catch (IOException e) {
            throw new IllegalArgumentException("The Polaris principal file is unavailable.");
        }
        config.put("header.X-Iceberg-Access-Delegation", "vended-credentials");
        super.initialize(name, config);
    }
}
