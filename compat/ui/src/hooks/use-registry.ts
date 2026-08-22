import { useState, useEffect } from "react";

export interface RegistryGroup {
  name: string;
  service: string;
  /** Restricts this group to specific suites — omitted for the normal case
   * where every SDK/CLI suite implements it. See registry.schema.json. */
  suites?: string[];
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
