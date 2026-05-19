import { createContext, useContext } from "react";
import type { Action } from "./reducer";

/**
 * Provides the reducer dispatch function to the component tree so hooks like
 * useRun can dispatch actions (e.g. "queued") without needing prop drilling.
 */
export const DispatchContext = createContext<React.Dispatch<Action> | null>(
  null,
);

export function useDispatchContext(): React.Dispatch<Action> {
  const dispatch = useContext(DispatchContext);
  if (!dispatch)
    throw new Error(
      "useDispatchContext must be called inside <DispatchContext.Provider>",
    );
  return dispatch;
}
