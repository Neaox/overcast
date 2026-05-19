import { createContext, useContext } from "react"

type DevToolsContextValue = { open: boolean; toggle: () => void }
export const DevToolsContext = createContext<DevToolsContextValue>({
  open: false,
  toggle: () => {},
})
export const useDevTools = () => useContext(DevToolsContext)
