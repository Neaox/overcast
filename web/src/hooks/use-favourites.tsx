import { createContext, useCallback, useContext } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"
import { ALL_SERVICES } from "@/lib/nav-services"

const FAVOURITES_KEY = "overcast-favourites"
const RECENT_KEY = "overcast-recent-services"
const MAX_RECENT = 8

interface FavouritesContextValue {
  /** Favourited service keys in display order — the array IS the order. */
  favourites: string[]
  toggleFavourite: (key: string) => void
  isFavourite: (key: string) => boolean
  reorderFavourites: (ordered: string[]) => void
  /** Recently visited service keys, most-recent first. */
  recentServices: string[]
  addRecentService: (key: string) => void
}

const FavouritesContext = createContext<FavouritesContextValue | null>(null)

export function FavouritesProvider({ children }: { children: React.ReactNode }) {
  const [favourites, setFavourites] = useLocalStorage<string[]>(FAVOURITES_KEY, [])
  const [recentServices, setRecentServices] = useLocalStorage<string[]>(RECENT_KEY, [])

  const toggleFavourite = useCallback(
    (key: string) => {
      const svcDef = ALL_SERVICES.find((s) => s.key === key)
      if (svcDef?.favouritable === false) return
      setFavourites((prev) => (prev.includes(key) ? prev.filter((k) => k !== key) : [...prev, key]))
    },
    [setFavourites],
  )

  const reorderFavourites = useCallback(
    (ordered: string[]) => {
      setFavourites(ordered)
    },
    [setFavourites],
  )

  const addRecentService = useCallback(
    (key: string) => {
      setRecentServices((prev) => [key, ...prev.filter((k) => k !== key)].slice(0, MAX_RECENT))
    },
    [setRecentServices],
  )

  return (
    <FavouritesContext.Provider
      value={{
        favourites,
        toggleFavourite,
        isFavourite: (key) => favourites.includes(key),
        reorderFavourites,
        recentServices,
        addRecentService,
      }}
    >
      {children}
    </FavouritesContext.Provider>
  )
}

// eslint-disable-next-line react-refresh/only-export-components
export function useFavourites() {
  const ctx = useContext(FavouritesContext)
  if (!ctx) throw new Error("useFavourites must be used within FavouritesProvider")
  return ctx
}
