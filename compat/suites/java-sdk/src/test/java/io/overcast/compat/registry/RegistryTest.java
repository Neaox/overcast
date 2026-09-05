package io.overcast.compat.registry;

import io.overcast.compat.harness.TestCase;
import io.overcast.compat.harness.TestFn;
import io.overcast.compat.harness.TestGroup;
import org.junit.jupiter.api.Test;

import java.util.List;
import java.util.Map;
import java.util.Set;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertNotNull;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertThrows;
import static org.junit.jupiter.api.Assertions.assertTrue;
import static org.junit.jupiter.api.Assertions.fail;

/**
 * Pins the impl-key resolution rules for the Java suite.
 *
 * <p>This suite spelled a group-qualified key {@code "group/test"} while every
 * other suite used {@code "group:test"}. A colon-form key therefore resolved to
 * nothing here and fell through to the bare test name — which, for a name that
 * several groups declare, is another group's implementation. The run reported a
 * pass for a test that never executed.
 */
class RegistryTest {

    private static final TestFn NOOP = ctx -> {};

    /**
     * Two unrelated groups declaring a test of the same name, plus a name owned
     * by exactly one group — the shape that made a mis-binding possible.
     */
    private static Registry.RegistryRoot twoGroupsOneName() {
        return new Registry.RegistryRoot(List.of(
                new Registry.RegistryGroup("iam", "iam-users", List.of(
                        new Registry.RegistryTest("ListUsers", null, null, null, null),
                        new Registry.RegistryTest("CreateUser", null, null, null, null))),
                new Registry.RegistryGroup("cognito", "cognito-userpools", List.of(
                        new Registry.RegistryTest("ListUsers", null, null, null, null)))));
    }

    private static TestCase find(List<TestGroup> groups, String group, String test) {
        for (TestGroup g : groups) {
            if (!g.name().equals(group)) continue;
            for (TestCase tc : g.tests()) {
                if (tc.name().equals(test)) return tc;
            }
        }
        fail("no test " + group + "/" + test + " in built groups");
        return null;
    }

    // ── Unresolved keys must abort, not warn ──────────────────────────────────

    @Test
    void rejectsKeyWrittenWithTheOldSlashSeparator() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.validateImpls(twoGroupsOneName(),
                        Map.of("iam-users/CreateUser", NOOP), "java-sdk"));

        assertTrue(e.getMessage().contains("iam-users/CreateUser"), e.getMessage());
        assertTrue(e.getMessage().contains("matches no registry entry"), e.getMessage());
        // The message must point at the colon form, since that is the fix.
        assertTrue(e.getMessage().contains("iam-users:CreateUser"), e.getMessage());
    }

    @Test
    void rejectsKeyNamingAnUnknownGroup() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.validateImpls(twoGroupsOneName(),
                        Map.of("iam-usres:CreateUser", NOOP), "java-sdk"));
        assertTrue(e.getMessage().contains("iam-usres:CreateUser"), e.getMessage());
    }

    @Test
    void rejectsKeyNamingAnUnknownTest() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.validateImpls(twoGroupsOneName(),
                        Map.of("CreateUsr", NOOP), "java-sdk"));
        assertTrue(e.getMessage().contains("CreateUsr"), e.getMessage());
    }

    // ── Cross-group binding must be refused ───────────────────────────────────

    @Test
    void rejectsBareKeyForANameSeveralGroupsDeclare() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.validateImpls(twoGroupsOneName(),
                        Map.of("ListUsers", NOOP), "java-sdk"));

        assertTrue(e.getMessage().contains("ambiguous"), e.getMessage());
        assertTrue(e.getMessage().contains("iam-users"), e.getMessage());
        assertTrue(e.getMessage().contains("cognito-userpools"), e.getMessage());
    }

    @Test
    void acceptsResolvableKeys() {
        Registry.validateImpls(twoGroupsOneName(), Map.of(
                "CreateUser", NOOP,                  // bare, single owner
                "iam-users:ListUsers", NOOP,
                "cognito-userpools:ListUsers", NOOP), "java-sdk");
    }

    /**
     * Defence in depth: even with validation bypassed, buildGroups must never
     * bind a group to another group's implementation via the bare fallback.
     */
    @Test
    void buildGroupsRefusesCrossGroupBareFallback() throws Exception {
        List<TestGroup> groups = Registry.buildGroups("java-sdk",
                twoGroupsOneName(), Map.of("ListUsers", NOOP), Map.of(), Map.of(), Set.of());

        TestCase cognito = find(groups, "cognito-userpools", "ListUsers");
        assertNotNull(cognito.skip(),
                "cognito-userpools/ListUsers bound to iam's impl; want unimplemented skip");
        assertEquals("not yet implemented in java-sdk test suite", cognito.skip());

        assertNotNull(find(groups, "iam-users", "ListUsers").skip(),
                "iam-users/ListUsers bound to an ambiguous bare impl; want skip");
    }

    @Test
    void buildGroupsBindsQualifiedKeyToItsGroupOnly() throws Exception {
        List<TestGroup> groups = Registry.buildGroups("java-sdk",
                twoGroupsOneName(), Map.of("iam-users:ListUsers", NOOP), Map.of(), Map.of(), Set.of());

        assertNull(find(groups, "iam-users", "ListUsers").skip(), "iam-users/ListUsers should be bound");
        assertNotNull(find(groups, "cognito-userpools", "ListUsers").skip(),
                "cognito-userpools/ListUsers bound to iam-users' impl");
    }

    /** The bare fallback must still serve a name only one group declares. */
    @Test
    void buildGroupsAllowsUnambiguousBareFallback() throws Exception {
        List<TestGroup> groups = Registry.buildGroups("java-sdk",
                twoGroupsOneName(), Map.of("CreateUser", NOOP), Map.of(), Map.of(), Set.of());
        assertNull(find(groups, "iam-users", "CreateUser").skip(),
                "iam-users/CreateUser should bind via its bare key");
    }

    // ── "suites" scoping applies to every group ───────────────────────────────

    /**
     * A hand-written group scoped to another suite is not loaded at all (#1737).
     *
     * <p>{@code cdk-lifecycle} is the only such group in the real registry, and
     * java-sdk is not in its {@code suites}: it contributes no tests, no skips
     * and no results here, which is what took its 35 rows out of
     * {@code compat/baseline/java-sdk.json}.
     */
    @Test
    void handWrittenGroupScopedToAnotherSuiteIsNotLoaded() {
        Registry.RegistryRoot root = new Registry.RegistryRoot(List.of(
                new Registry.RegistryGroup("s3", "s3-crud", List.of(
                        new Registry.RegistryTest("CreateBucket", null, null, null, null))),
                new Registry.RegistryGroup("cdk", "cdk-lifecycle", List.of(
                        new Registry.RegistryTest("Deploy", null, null, null, null)),
                        List.of("cdk"), false, null, null)));

        List<TestGroup> groups = Registry.buildGroups("java-sdk",
                root, Map.of(), Map.of(), Map.of(), Set.of());

        assertEquals(List.of("s3-crud"), groups.stream().map(TestGroup::name).toList());
    }

    /** The same group is loaded by the suite it names. */
    @Test
    void handWrittenGroupIsLoadedBySuiteItNames() {
        Registry.RegistryRoot root = new Registry.RegistryRoot(List.of(
                new Registry.RegistryGroup("cdk", "cdk-lifecycle", List.of(
                        new Registry.RegistryTest("Deploy", null, null, null, null)),
                        List.of("cdk"), false, null, null)));

        List<TestGroup> groups = Registry.buildGroups("cdk",
                root, Map.of(), Map.of(), Map.of(), Set.of());

        assertEquals(List.of("cdk-lifecycle"), groups.stream().map(TestGroup::name).toList());
    }

    // ── Duplicate registrations must abort ────────────────────────────────────

    /**
     * Two service groups registering the same key must abort the run.
     *
     * <p>This is the gap {@code validateImpls} cannot close. The merge that
     * builds the suite's impl map is last-writer-wins, so one of the two
     * implementations is discarded before validation ever sees the map — and
     * the surviving key resolves perfectly well, so nothing is reported. The
     * discarded test then runs the other class's implementation under its own
     * name.
     */
    @Test
    void rejectsKeyRegisteredByTwoSources() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.mergeImpls(List.of(
                        new Registry.ImplSource("LambdaGroup",
                                Map.of("lambda-crud:CreateFunction", NOOP)),
                        new Registry.ImplSource("AppSyncGroup",
                                Map.of("lambda-crud:CreateFunction", NOOP))), "java-sdk"));

        assertTrue(e.getMessage().contains("duplicate impl registration"), e.getMessage());
        assertTrue(e.getMessage().contains("lambda-crud:CreateFunction"), e.getMessage());
        // Both registering classes must be named: the key alone does not say
        // where to look, and one of the two classes is in the wrong.
        assertTrue(e.getMessage().contains("LambdaGroup"), e.getMessage());
        assertTrue(e.getMessage().contains("AppSyncGroup"), e.getMessage());
    }

    /** "both X and Y" would be nonsense when X and Y are the same class. */
    @Test
    void reportsSingleSourceDuplicateAsSuch() {
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.mergeImpls(List.of(
                        new Registry.ImplSource("IamGroup", Map.of("iam-users:CreateUser", NOOP)),
                        new Registry.ImplSource("IamGroup", Map.of("iam-users:CreateUser", NOOP))),
                        "java-sdk"));

        assertTrue(e.getMessage().contains("registered twice by \"IamGroup\""), e.getMessage());
    }

    /** Fixing one duplicate must not merely reveal the next. */
    @Test
    void reportsEveryDuplicate() {
        Map<String, TestFn> both = Map.of(
                "iam-users:ListUsers", NOOP,
                "iam-users:CreateUser", NOOP);
        IllegalStateException e = assertThrows(IllegalStateException.class,
                () -> Registry.mergeImpls(List.of(
                        new Registry.ImplSource("IamGroup", both),
                        new Registry.ImplSource("CognitoGroup", both)), "java-sdk"));

        assertTrue(e.getMessage().contains("2 duplicate impl registration(s)"), e.getMessage());
        // Sorted by key, so the message is stable however the maps iterate.
        assertTrue(e.getMessage().indexOf("iam-users:CreateUser")
                        < e.getMessage().indexOf("iam-users:ListUsers"),
                "problems not sorted by key: " + e.getMessage());
    }

    /** Negative control: distinct keys merge, each keeping its own impl. */
    @Test
    void acceptsDisjointSources() {
        TestFn iamList = ctx -> {};
        TestFn iamCreate = ctx -> {};
        TestFn cognitoList = ctx -> {};

        Map<String, TestFn> merged = Registry.mergeImpls(List.of(
                new Registry.ImplSource("IamGroup", Map.of(
                        "iam-users:ListUsers", iamList,
                        "CreateUser", iamCreate)),
                new Registry.ImplSource("CognitoGroup", Map.of(
                        "cognito-userpools:ListUsers", cognitoList))), "java-sdk");

        assertEquals(3, merged.size(), merged.keySet().toString());
        assertEquals(iamList, merged.get("iam-users:ListUsers"));
        assertEquals(iamCreate, merged.get("CreateUser"));
        assertEquals(cognitoList, merged.get("cognito-userpools:ListUsers"));
    }

    @Test
    void ambiguousTestNamesTracksOwners() {
        Registry.RegistryRoot root = twoGroupsOneName();
        assertTrue(Registry.ambiguousTestNames(root).contains("ListUsers"));
        assertTrue(!Registry.ambiguousTestNames(root).contains("CreateUser"));
        assertEquals(List.of("cognito-userpools", "iam-users"),
                Registry.testNameOwners(root).get("ListUsers"));
    }
}
