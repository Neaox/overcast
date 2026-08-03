package io.overcast.compat;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.groups.ServiceGroup;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.registry.Registry;
import org.junit.jupiter.api.Test;

import java.util.LinkedHashMap;
import java.util.Map;

/**
 * The suite's own registrations must resolve against the real registry.json.
 *
 * <p>This is the check that catches a mis-binding before a run reports one: an
 * unresolvable key (a typo, or the {@code "group/test"} form this suite used
 * before the separator was unified), or a bare key for a name that several
 * groups declare and which therefore binds an arbitrary one of them.
 *
 * <p>The registry is read from {@code ../registry.json}, which resolves both
 * when Maven runs from {@code compat/suites/java-sdk/} and in the Docker build,
 * whose working directory is {@code /build} with the registry at {@code /}.
 */
class MainRegistrationTest {

    @Test
    void registeredImplsResolveAgainstRegistry() throws Exception {
        AwsClients clients = new AwsClients("http://127.0.0.1:1", "us-east-1");

        Map<String, TestFn> impls = new LinkedHashMap<>();
        for (ServiceGroup sg : Main.serviceGroups(clients)) {
            impls.putAll(sg.impls());
        }

        // Throws IllegalStateException naming every unusable key.
        Registry.validateImpls(impls, "java-sdk");
    }
}
