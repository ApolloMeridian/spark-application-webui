package org.sparkconsole;

import java.nio.charset.StandardCharsets;
import java.security.MessageDigest;
import java.time.Instant;
import java.util.Base64;
import javax.crypto.Mac;
import javax.crypto.spec.SecretKeySpec;
import javax.security.sasl.AuthenticationException;
import org.apache.kyuubi.service.authentication.PasswdAuthenticationProvider;

public final class ConsoleKyuubiAuth implements PasswdAuthenticationProvider {
    @Override
    public void authenticate(String user, String password) throws AuthenticationException {
        try {
            String key = System.getenv("SPARK_CONSOLE_ENGINE_SIGNING_KEY");
            if (key == null || key.getBytes(StandardCharsets.UTF_8).length < 32 ||
                    user == null || !user.matches("console_[0-9a-f]{32}") ||
                    password == null || !password.matches("[0-9]{1,12}\\.[A-Za-z0-9_-]{43}")) {
                throw new AuthenticationException("Invalid console identity");
            }
            String[] token = password.split("\\.", 2);
            long now = Instant.now().getEpochSecond();
            long expiry = Long.parseLong(token[0]);
            Mac mac = Mac.getInstance("HmacSHA256");
            mac.init(new SecretKeySpec(key.getBytes(StandardCharsets.UTF_8), "HmacSHA256"));
            byte[] expected = mac.doFinal(("spark-console-kyuubi-v1\n" + user + "\n" + token[0])
                    .getBytes(StandardCharsets.UTF_8));
            if (expiry <= now || expiry > now + 60 ||
                    !MessageDigest.isEqual(expected, Base64.getUrlDecoder().decode(token[1]))) {
                throw new AuthenticationException("Invalid console identity");
            }
        } catch (Exception e) {
            throw new AuthenticationException("Invalid console identity");
        }
    }
}
