import { useEffect } from "react";
import type { Action } from "../state/reducer";
import { useRegistry } from "./use-registry";

/**
 * Fetches the canonical test registry (/registry) and dispatches a
 * seed_registry action so every group and test appears in the result
 * grid immediately — even before any suite has been triggered.
 *
 * The seed is a pure structural insert: groups/tests already populated
 * by live events are never overwritten. Cells stay empty (un-run)
 * until a suite produces real test_result events.
 *
 * Handles fetch failures silently — the registry is a cosmetic boost,
 * not a hard dependency. The UI remains functional without it.
 */
export function useRegistrySeed(dispatch: React.Dispatch<Action>): void {
  const registry = useRegistry();

  useEffect(() => {
    if (registry && registry.groups.length > 0) {
      dispatch({ type: "seed_registry", groups: registry.groups });
    }
  }, [registry, dispatch]);
}
