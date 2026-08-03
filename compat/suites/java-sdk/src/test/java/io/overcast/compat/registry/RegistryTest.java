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

    @Test
    void ambiguousTestNamesTracksOwners() {
        Registry.RegistryRoot root = twoGroupsOneName();
        assertTrue(Registry.ambiguousTestNames(root).contains("ListUsers"));
        assertTrue(!Registry.ambiguousTestNames(root).contains("CreateUser"));
        assertEquals(List.of("cognito-userpools", "iam-users"),
                Registry.testNameOwners(root).get("ListUsers"));
    }
}
