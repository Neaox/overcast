import { useEffect } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"

type Theme = "light" | "dark" | "system"

const STORAGE_KEY = "overcast:theme"

function applyTheme(theme: Theme) {
  const root = document.documentElement
  if (theme === "system") {
    root.removeAttribute("data-theme")
  } else {
    root.setAttribute("data-theme", theme)
  }
}

export function useTheme() {
  const [theme, setTheme] = useLocalStorage<Theme>(STORAGE_KEY, "system")

  useEffect(() => {
    applyTheme(theme)
  }, [theme])

  return { theme, setTheme }
}
