import { renderHook } from "@testing-library/react"
import { useLoadMoreAtEdge, type LoadMoreAtEdgeOptions } from "./use-load-more-at-edge"

function options(overrides: Partial<LoadMoreAtEdgeOptions> = {}): LoadMoreAtEdgeOptions {
  return {
    firstIndex: 0,
    lastIndex: 99,
    count: 100,
    edge: "end",
    nextPageToken: "token-1",
    isFetchingNextPage: false,
    fetchNextPage: vi.fn(),
    ...overrides,
  }
}

describe("useLoadMoreAtEdge", () => {
  it("fetches when the user is within the threshold of the end", () => {
    const fetchNextPage = vi.fn()
    renderHook(() => useLoadMoreAtEdge(options({ lastIndex: 95, fetchNextPage })))
    expect(fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it("does not fetch while the user is far from the edge", () => {
    const fetchNextPage = vi.fn()
    renderHook(() => useLoadMoreAtEdge(options({ lastIndex: 40, fetchNextPage })))
    expect(fetchNextPage).not.toHaveBeenCalled()
  })

  it("does not fetch when no further page exists", () => {
    const fetchNextPage = vi.fn()
    renderHook(() => useLoadMoreAtEdge(options({ nextPageToken: undefined, fetchNextPage })))
    expect(fetchNextPage).not.toHaveBeenCalled()
  })

  it("does not stack a second fetch on top of one in flight", () => {
    const fetchNextPage = vi.fn()
    renderHook(() => useLoadMoreAtEdge(options({ isFetchingNextPage: true, fetchNextPage })))
    expect(fetchNextPage).not.toHaveBeenCalled()
  })

  it("does not fetch while disabled", () => {
    const fetchNextPage = vi.fn()
    renderHook(() => useLoadMoreAtEdge(options({ enabled: false, fetchNextPage })))
    expect(fetchNextPage).not.toHaveBeenCalled()
  })

  it("chains page to page when a response commits no fetching-state render", () => {
    // The race this hook exists for: the fetch resolves so fast that no
    // render with isFetchingNextPage: true ever commits. An effect keyed on
    // the booleans sees identical deps and stalls; the token differs per
    // page, so each arrival re-arms the effect.
    const fetchNextPage = vi.fn()
    const { rerender } = renderHook((props: LoadMoreAtEdgeOptions) => useLoadMoreAtEdge(props), {
      initialProps: options({ fetchNextPage, nextPageToken: "page-1" }),
    })
    expect(fetchNextPage).toHaveBeenCalledTimes(1)

    // Page 2 lands: same booleans, same edge position, new token.
    rerender(options({ fetchNextPage, nextPageToken: "page-2", count: 200, lastIndex: 199 }))
    expect(fetchNextPage).toHaveBeenCalledTimes(2)
  })

  it("fetches once per page, not once per scroll frame near the edge", () => {
    const fetchNextPage = vi.fn()
    const { rerender } = renderHook((props: LoadMoreAtEdgeOptions) => useLoadMoreAtEdge(props), {
      initialProps: options({ fetchNextPage, lastIndex: 95 }),
    })
    // Scroll frames move the rendered range but stay near the edge; the
    // token has not changed, so nothing re-fires.
    rerender(options({ fetchNextPage, lastIndex: 96 }))
    rerender(options({ fetchNextPage, lastIndex: 97 }))
    expect(fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it("re-arms after the user leaves and returns to the edge of the same page", () => {
    const fetchNextPage = vi.fn()
    const { rerender } = renderHook((props: LoadMoreAtEdgeOptions) => useLoadMoreAtEdge(props), {
      initialProps: options({ fetchNextPage, lastIndex: 95 }),
    })
    rerender(options({ fetchNextPage, lastIndex: 40 }))
    rerender(options({ fetchNextPage, lastIndex: 95 }))
    // Same token both times: the retry after scrolling away and back is
    // deliberate — the first fetch may have failed without changing the token.
    expect(fetchNextPage).toHaveBeenCalledTimes(2)
  })

  it("watches the start of the list when the edge is 'start'", () => {
    const fetchNextPage = vi.fn()
    const { rerender } = renderHook((props: LoadMoreAtEdgeOptions) => useLoadMoreAtEdge(props), {
      initialProps: options({ edge: "start", firstIndex: 50, lastIndex: 80, fetchNextPage }),
    })
    expect(fetchNextPage).not.toHaveBeenCalled()

    rerender(options({ edge: "start", firstIndex: 5, lastIndex: 35, fetchNextPage }))
    expect(fetchNextPage).toHaveBeenCalledTimes(1)
  })

  it("does not fetch when nothing renders at all", () => {
    // No rows rendered (empty list, zero-height container): there is no edge
    // to be near, and firing here would loop an empty query forever.
    const fetchNextPage = vi.fn()
    renderHook(() =>
      useLoadMoreAtEdge(
        options({ edge: "start", firstIndex: undefined, lastIndex: undefined, fetchNextPage }),
      ),
    )
    expect(fetchNextPage).not.toHaveBeenCalled()
  })

  it("treats an empty list at the end edge as near it", () => {
    // The end-edge threshold compares against count: with nothing loaded yet
    // every index is within threshold of the end, so the first page loads
    // without the user doing anything. That matches the previous inline
    // effects, which fired as soon as any row rendered.
    const fetchNextPage = vi.fn()
    renderHook(() =>
      useLoadMoreAtEdge(options({ count: 3, firstIndex: 0, lastIndex: 2, fetchNextPage })),
    )
    expect(fetchNextPage).toHaveBeenCalledTimes(1)
  })
})
