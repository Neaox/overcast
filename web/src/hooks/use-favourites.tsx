import { createContext, useCallback, useContext } from "react"
import { useLocalStorage } from "@/hooks/use-local-storage"
import { useServiceAvailability } from "@/hooks/use-service-availability"
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

/**
 * Pins are stored per user, but a run that enables only some services starts
 * from those services rather than from nothing — on a narrowed emulator the
 * enabled set is the useful sidebar. Those defaults are suggestions, not
 * state: nothing is written until the user pins, unpins, or reorders, which is
 * why `null` (never chosen) is kept distinct from `[]` (chose to pin nothing).
 */
export function FavouritesProvider({ children }: { children: React.ReactNode }) {
  const [pinned, setPinned] = useLocalStorage<string[] | null>(FAVOURITES_KEY, null)
  const [recentServices, setRecentServices] = useLocalStorage<string[]>(RECENT_KEY, [])
  const { defaultPins } = useServiceAvailability()

  const favourites = pinned ?? defaultPins

  // Editing a defaulted list commits it: the user is adjusting these pins, not
  // opting out of them.
  const toggleFavourite = useCallback(
    (key: string) => {
      const svcDef = ALL_SERVICES.find((s) => s.key === key)
      if (svcDef?.favouritable === false) return
      setPinned((prev) => {
        const current = prev ?? defaultPins
        return current.includes(key) ? current.filter((k) => k !== key) : [...current, key]
      })
    },
    [setPinned, defaultPins],
  )

  const reorderFavourites = useCallback(
    (ordered: string[]) => {
      setPinned(ordered)
    },
    [setPinned],
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
