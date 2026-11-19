package org.sparkconsole;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.Base64;
import java.util.Collections;
import java.util.HashMap;
import java.util.Map;
import java.util.WeakHashMap;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import org.apache.spark.sql.SparkSession;

final class DriverPolicy {
    static final String TICKET = "spark.sql.sparkconsole.authorizationToken";
    static final String SESSION = "spark.sql.sparkconsole.session";
    private static final Map<SparkSession, DriverPolicy> POLICIES = Collections.synchronizedMap(new WeakHashMap<>());
    private final String sqlHash;
    private final Map<String, String> bindings;
    private final long expires;

    private DriverPolicy(String sqlHash, Map<String, String> bindings, long expires) {
        this.sqlHash = sqlHash;
        this.bindings = bindings;
        this.expires = expires;
    }

    static void install(SparkSession session, String ticket) {
        // Keep the token outside Spark SQLConf, which can be captured in views.
        var conf = session.conf();
        String catalog = conf.get("spark.sql.catalog.polaris.uri", "").replaceAll("/+$", "") + "\n" +
                conf.get("spark.sql.catalog.polaris.header.Polaris-Realm", "") + "\n" +
                conf.get("spark.sql.catalog.polaris.warehouse", "");
        POLICIES.put(session, verify(ticket, conf.get(SESSION, ""), hash(catalog)));
    }

    static DriverPolicy forSession(SparkSession session) {
        DriverPolicy installed = POLICIES.get(session);
        if (installed == null || installed.expires <= Instant.now().getEpochSecond()) throw denied();
        return installed;
    }

    static DriverPolicy current() {
        // View expansion temporarily substitutes its captured SQLConf. Use
        // the active Spark session's current ticket for all catalog access.
        var active = SparkSession.getActiveSession();
        if (active.isEmpty()) throw denied();
        return forSession(active.get());
    }

    private static DriverPolicy verify(String ticket, String session, String catalogHash) {
        try {
            String key = System.getenv("SPARK_CONSOLE_ENGINE_SIGNING_KEY");
            if (key == null || key.length() < 32 || session.isEmpty() || ticket.length() > 65536) throw denied();
            String[] parts = ticket.split("\\.", -1);
            if (parts.length != 2) throw denied();
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            byte[] expected = mac.doFinal(("spark-console-driver-v1\n" + parts[0]).getBytes(StandardCharsets.UTF_8));
            if (!MessageDigest.isEqual(expected, Base64.getUrlDecoder().decode(parts[1]))) throw denied();
            String[] fields = new String(Base64.getUrlDecoder().decode(parts[0]), StandardCharsets.UTF_8).split("\n", -1);
            long now = Instant.now().getEpochSecond();
            if (fields.length != 9) throw denied();
            long expiry = Long.parseLong(fields[6]);
            if (!fields[0].equals(session) || fields[1].isEmpty() || fields[2].isEmpty() || !fields[3].equals(catalogHash) ||
                    !fields[4].matches("[a-f0-9]{64}") || !fields[5].matches("[a-f0-9]{64}") ||
                    expiry <= now || expiry > now + 1860 || fields[7].isEmpty()) throw denied();
            Map<String, String> bindings = new HashMap<>();
            if (!fields[8].equals("-")) {
                for (String pair : fields[8].split(",")) {
                    String[] binding = pair.split("=", -1);
                    if (binding.length != 2 || !binding[1].matches("[a-f0-9]{8}(-[a-f0-9]{4}){3}-[a-f0-9]{12}(:[0-9]+)?")) throw denied();
                    if (bindings.put(binding[0], binding[1]) != null) throw denied();
                }
            }
            return new DriverPolicy(fields[5], Map.copyOf(bindings), expiry);
        } catch (Exception failure) {
            throw denied();
        }
    }

    boolean accepts(String sql) {
        return sqlHash.equals(hash(sql));
    }

    private static String hash(String value) {
        try {
            byte[] digest = MessageDigest.getInstance("SHA-256").digest(value.getBytes(StandardCharsets.UTF_8));
            StringBuilder hash = new StringBuilder(64);
            for (byte octet : digest) hash.append(String.format("%02x", octet));
            return hash.toString();
        } catch (Exception failure) {
            throw denied();
        }
    }

    String binding(String resource) {
        return bindings.get(resource);
    }

    static IllegalArgumentException denied() {
        return new IllegalArgumentException("Spark Console authorization ticket is missing, invalid, or expired.");
    }
}
