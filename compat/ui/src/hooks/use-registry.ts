import { useState, useEffect } from "react";

export interface RegistryGroup {
  name: string;
  service: string;
  /** Restricts this group to specific suites — omitted for the normal case
   * where every SDK/CLI suite implements it. See registry.schema.json. */
  suites?: string[];
  /** True for groups from registry.generated.json, which GET /registry
   * concatenates onto the hand-written registry. Absent on hand-written
   * groups.
   *
   * Tracked in #1113: faceting on this is the remaining half of the plan's
   * dashboard item — the matrix should default to hand-written groups and
   * lead with model coverage rather than rendering thousands of generated
   * rows. See docs/plans/compat-coverage-modelgen.md § 3.6. */
  generated?: boolean;
  /** "candidate" groups report everywhere but gate nothing; "gated" ones are
   * enforced like hand-written groups. Only set when `generated` is true. */
  state?: "candidate" | "gated";
  /** Scenario IR file the group was generated from. Only set when `generated`
   * is true. */
  scenario?: string;
  tests: Array<{
    name: string;
    op?: string;
    depends?: string[];
  }>;
}

export interface Registry {
  groups: RegistryGroup[];
}

export function useRegistry(): Registry | null {
  const [registry, setRegistry] = useState<Registry | null>(null);
  useEffect(() => {
    fetch("/registry")
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => {
        if (data) setRegistry(data);
      })
      .catch(() => {});
  }, []);
  return registry;
}
