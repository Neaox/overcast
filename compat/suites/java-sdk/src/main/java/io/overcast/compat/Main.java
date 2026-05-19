package io.overcast.compat;

import io.overcast.compat.clients.AwsClients;
import io.overcast.compat.groups.*;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.harness.TestGroup;
import io.overcast.compat.harness.Runner;
import io.overcast.compat.harness.InteractiveRunner;
import io.overcast.compat.registry.Registry;

import java.util.*;

/**
 * Entry point for the Overcast Java SDK v2 compatibility suite.
 *
 * <p>Reads configuration from environment variables, wires all service groups,
 * loads the shared {@code registry.json}, and runs the suite via
 * {@link Runner#runSuite} which emits NDJSON to {@code stdout}.
 *
 * <h2>Environment variables</h2>
 * <table>
 *   <tr><td>{@code OVERCAST_ENDPOINT}</td>         <td>Overcast base URL (default: http://localhost:4566)</td></tr>
 *   <tr><td>{@code OVERCAST_DEFAULT_REGION}</td>            <td>AWS region (default: us-east-1)</td></tr>
 *   <tr><td>{@code OVERCAST_COMPAT_RUN_ID}</td>     <td>Unique run ID; all resources must be prefixed
 *                                                   with this value so the orphan sweep can find them</td></tr>
 *   <tr><td>{@code OVERCAST_COMPAT_SKIP_DOCKER}</td><td>Set to "1" to skip tests that require Docker</td></tr>
 *   <tr><td>{@code OVERCAST_COMPAT_GROUPS}</td>     <td>Comma-separated group names to run (default: all)</td></tr>
 *   <tr><td>{@code OVERCAST_COMPAT_SERVICE}</td>    <td>AWS service name to run (default: all)</td></tr>
 *   <tr><td>{@code OVERCAST_COMPAT_TESTS}</td>      <td>Comma-separated test names to run (default: all)</td></tr>
 *   <tr><td>{@code OVERCAST_REGISTRY_PATH}</td>     <td>Override path to registry.json</td></tr>
 * </table>
 */
public final class Main {

    private static final String SUITE = "java-sdk";

    public static void main(String[] args) {
        // Kinesis uses CBOR by default in the Java SDK v2; Overcast only supports JSON.
        System.setProperty("aws.cborEnabled", "false");
        String endpoint   = env("OVERCAST_ENDPOINT",  "http://localhost:4566");
        boolean skipDocker = "1".equals(System.getenv("OVERCAST_COMPAT_SKIP_DOCKER"));

        AwsClients clients = new AwsClients(endpoint, env("OVERCAST_DEFAULT_REGION", "us-east-1"));

        // ── Collect impls / setups / teardowns from all service groups ─────────
        Map<String, TestFn> impls     = new LinkedHashMap<>();
        Map<String, TestFn> setups    = new LinkedHashMap<>();
        Map<String, TestFn> teardowns = new LinkedHashMap<>();

        List<ServiceGroup> serviceGroups = List.of(
                new S3Group(clients),
                new SqsGroup(clients),
                new DynamoDbGroup(clients),
                new SnsGroup(clients),
                new LambdaGroup(clients),
                new StsGroup(clients),
                new KmsGroup(clients),
                new SecretsManagerGroup(clients),
                new SsmGroup(clients),
                new IamGroup(clients),
                new KinesisGroup(clients),
                new CloudWatchLogsGroup(clients),
                new SesGroup(clients),
                new EventBridgeGroup(clients),
                new CloudFormationGroup(clients),
                new Ec2Group(clients),
                new EcsGroup(clients),
                new CognitoGroup(clients),
                new AppSyncGroup(clients),
                new ApiGatewayGroup(clients),
                new CloudFrontGroup(clients),
                new RdsGroup(clients),
                new StepFunctionsGroup(clients),
                new WafGroup(clients),
                new ShieldGroup(clients)
        );

        for (ServiceGroup sg : serviceGroups) {
            impls.putAll(sg.impls());
            setups.putAll(sg.setups());
            teardowns.putAll(sg.teardowns());
        }

        // ── Build capabilities set ─────────────────────────────────────────────
        Set<String> capabilities = new HashSet<>();
        if (!skipDocker) {
            capabilities.add("docker");
        }

        // ── Load registry and build groups ─────────────────────────────────────
        List<TestGroup> allGroups;
        try {
            allGroups = Registry.buildGroups(SUITE, impls, setups, teardowns, capabilities);
        } catch (Exception e) {
            System.err.println("[java-sdk] failed to load registry: " + e.getMessage());
            System.exit(1);
            return;
        }

        // ── Apply filters ──────────────────────────────────────────────────────
        Set<String> filterServices = splitFilter(System.getenv("OVERCAST_COMPAT_SERVICE"));
        Set<String> filterGroups   = splitFilter(System.getenv("OVERCAST_COMPAT_GROUPS"));
        Set<String> filterTests    = splitFilter(System.getenv("OVERCAST_COMPAT_TESTS"));

        List<TestGroup> groups = allGroups;
        if (!filterServices.isEmpty()) {
            groups = groups.stream()
                    .filter(g -> filterServices.contains(g.service()))
                    .toList();
        }
        if (!filterGroups.isEmpty()) {
            groups = groups.stream()
                    .filter(g -> filterGroups.contains(g.name()))
                    .toList();
        }
        if (!filterTests.isEmpty()) {
            groups = groups.stream()
                    .map(g -> {
                        var tests = g.tests().stream()
                                .filter(tc -> filterTests.contains(tc.name()))
                                .toList();
                        return tests.isEmpty() ? null
                                : new TestGroup(g.suite(), g.service(), g.name(), tests,
                                                g.setup(), g.teardown());
                    })
                    .filter(Objects::nonNull)
                    .toList();
        }

        // ── Run ────────────────────────────────────────────────────────────────
        if ("1".equals(System.getenv("OVERCAST_COMPAT_INTERACTIVE"))) {
            InteractiveRunner.run(SUITE, endpoint, allGroups);
        } else {
            Runner.runSuite(SUITE, endpoint, groups);
        }
    }

    // ── Helpers ────────────────────────────────────────────────────────────────

    private static String env(String name, String defaultValue) {
        String v = System.getenv(name);
        return (v != null && !v.isBlank()) ? v : defaultValue;
    }

    private static Set<String> splitFilter(String value) {
        if (value == null || value.isBlank()) return Set.of();
        Set<String> set = new HashSet<>();
        for (String s : value.split(",")) {
            String t = s.trim();
            if (!t.isEmpty()) set.add(t);
        }
        return Set.copyOf(set);
    }
}
