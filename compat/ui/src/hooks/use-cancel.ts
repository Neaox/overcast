import { useCallback } from "react";

export interface CancelFilter {
  batch_id?: string;
  suite?: string;
  group?: string;
  test?: string;
  all?: boolean;
}

export function useCancel() {
  return useCallback(async (filter: CancelFilter = {}): Promise<boolean> => {
    const res = await fetch("/cancel", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(filter),
    }).catch(() => null);
    return res?.ok ?? false;
  }, []);
}
