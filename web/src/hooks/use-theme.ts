import { useEffect, useState } from "react"

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
  const [theme, setThemeState] = useState<Theme>(() => {
    return (localStorage.getItem(STORAGE_KEY) as Theme | null) ?? "system"
  })

  useEffect(() => {
    applyTheme(theme)
    localStorage.setItem(STORAGE_KEY, theme)
  }, [theme])

  // Apply on mount (before first render flicker)
  useEffect(() => {
    applyTheme(theme)
  }, []) // eslint-disable-line react-hooks/exhaustive-deps

  return { theme, setTheme: setThemeState }
}
