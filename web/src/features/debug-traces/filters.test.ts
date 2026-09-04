import { describe, expect, it } from "vitest"
import { debugTraceKeys } from "./data"
import {
  filterListParam,
  serviceKey,
  showsTrace,
  traceListParams,
  validateTracesSearch,
  type TracesSearch,
} from "./filters"
import { traceListQuery } from "@/services/api/misc"
import { checkboxFilterLabel, toggleFilterValue } from "@/components/ui/use-checkbox-filter"

describe("validateTracesSearch", () => {
  it("resolves every key for an empty URL, so nothing is written back", () => {
    expect(validateTracesSearch({})).toEqual({})
  })

  it("names every key even when absent, so a rejected raw param cannot survive the merge", () => {
    // The router merges this over the raw search params; an omitted key would
    // leave `?status=6xx` in place as the string it was just rejected as.
    expect(Object.keys(validateTracesSearch({})).sort()).toEqual([
      "hideServices",
      "method",
      "search",
      "showInternal",
      "status",
    ])
  })

  it("accepts a repeated param as an array and canonicalises it", () => {
    // Given: the shape TanStack Router round-trips an array through the URL,
    // in the order the user happened to tick the boxes.
    const parsed = validateTracesSearch({ status: ["5xx", "4xx", "4xx"] })

    // Then: deduped and sorted, so tick order never changes the URL or key.
    expect(parsed.status).toEqual(["4xx", "5xx"])
  })

  it("accepts a comma-separated value, which the server also flattens", () => {
    expect(validateTracesSearch({ status: "4xx,5xx" }).status).toEqual(["4xx", "5xx"])
    expect(validateTracesSearch({ method: "get, post" }).method).toEqual(["GET", "POST"])
  })

  it("accepts a single value, exactly as the old single-select produced", () => {
    expect(validateTracesSearch({ status: "2xx" }).status).toEqual(["2xx"])
  })

  it("normalises case: statuses lower, methods upper", () => {
    expect(validateTracesSearch({ status: "4XX" }).status).toEqual(["4xx"])
    expect(validateTracesSearch({ method: "delete" }).method).toEqual(["DELETE"])
  })

  it("keeps an exact numeric status code, which the server accepts too", () => {
    expect(validateTracesSearch({ status: "404,5xx" }).status).toEqual(["404", "5xx"])
  })

  it("drops malformed entries instead of throwing, so a hand-edited URL still renders", () => {
    const parsed = validateTracesSearch({
      status: ["6xx", "<script>", "", "4xx", 500, null],
      method: ["GET", "P0ST", { evil: true }],
      search: "   ",
      showInternal: "maybe",
      hideServices: 42,
    })

    expect(parsed).toEqual({ status: ["4xx"], method: ["GET"] })
  })

  it("survives values that are not strings or arrays at all", () => {
    expect(() =>
      validateTracesSearch({ status: { $ne: null }, method: 7, search: [] }),
    ).not.toThrow()
    expect(validateTracesSearch({ status: { $ne: null }, method: 7, search: [] })).toEqual({})
  })

  it("takes any service name for the hide-list, since services are discovered at runtime", () => {
    expect(validateTracesSearch({ hideServices: "s3,(unknown)" }).hideServices).toEqual([
      "(unknown)",
      "s3",
    ])
  })

  it("treats showInternal as opt-in, so the default hides internal traces", () => {
    expect(validateTracesSearch({}).showInternal).toBeUndefined()
    expect(validateTracesSearch({ showInternal: false }).showInternal).toBeUndefined()
    expect(validateTracesSearch({ showInternal: "true" }).showInternal).toBe(true)
    expect(validateTracesSearch({ showInternal: true }).showInternal).toBe(true)
  })

  it("round-trips a fully populated filter set unchanged", () => {
    const filters: TracesSearch = {
      search: "PutObject",
      status: ["4xx", "5xx"],
      method: ["GET", "POST"],
      hideServices: ["sqs"],
      showInternal: true,
    }

    expect(validateTracesSearch({ ...filters })).toEqual(filters)
  })
})

describe("filterListParam", () => {
  it("drops an empty selection so the param leaves the URL", () => {
    expect(filterListParam([])).toBeUndefined()
    expect(filterListParam(["4xx"])).toEqual(["4xx"])
  })
})

describe("traceListParams", () => {
  it("sends only the server-side filters", () => {
    const params = traceListParams(
      {
        search: "s3",
        status: ["4xx", "5xx"],
        method: ["GET"],
        // Applied to rows already fetched: including these would refetch every
        // page whenever a service is hidden.
        hideServices: ["sqs"],
        showInternal: true,
      },
      50,
    )

    expect(params).toEqual({ limit: 50, search: "s3", status: ["4xx", "5xx"], method: ["GET"] })
  })

  it("omits absent filters entirely", () => {
    expect(traceListParams({}, 50)).toEqual({ limit: 50 })
  })

  it("produces an order-independent query key", () => {
    const a = traceListParams(validateTracesSearch({ status: ["4xx", "5xx"] }), 50)
    const b = traceListParams(validateTracesSearch({ status: ["5xx", "4xx"] }), 50)

    expect(debugTraceKeys.list(a)).toEqual(debugTraceKeys.list(b))
  })
})

describe("traceListQuery", () => {
  it("repeats status and method rather than joining them, so the server matches any", () => {
    const query = traceListQuery({ status: ["4xx", "5xx"], method: ["GET", "POST"], limit: 50 })

    expect(query).toBe("?method=GET&method=POST&status=4xx&status=5xx&limit=50")
    expect(new URLSearchParams(query.slice(1)).getAll("status")).toEqual(["4xx", "5xx"])
  })

  it("emits a single value the same way the old single-select filter did", () => {
    expect(traceListQuery({ status: ["4xx"] })).toBe("?status=4xx")
  })

  it("omits repeated params entirely when nothing is selected", () => {
    expect(traceListQuery({ status: [], method: [], limit: 50 })).toBe("?limit=50")
    expect(traceListQuery()).toBe("")
  })

  it("still carries the cursor and free-text params", () => {
    const query = traceListQuery({ search: "a b", after: "req-1", limit: 50 })

    const parsed = new URLSearchParams(query.slice(1))
    expect(parsed.get("search")).toBe("a b")
    expect(parsed.get("after")).toBe("req-1")
  })
})

describe("toggleFilterValue", () => {
  it("adds in sorted order regardless of click order", () => {
    expect(toggleFilterValue(toggleFilterValue([], "5xx"), "4xx")).toEqual(["4xx", "5xx"])
    expect(toggleFilterValue(toggleFilterValue([], "4xx"), "5xx")).toEqual(["4xx", "5xx"])
  })

  it("removes an already-selected id", () => {
    expect(toggleFilterValue(["4xx", "5xx"], "4xx")).toEqual(["5xx"])
  })
})

describe("checkboxFilterLabel", () => {
  it("describes what the table shows, not which boxes are ticked", () => {
    // Show-model: nothing ticked filters nothing out.
    expect(checkboxFilterLabel("show", 4, 0, "statuses")).toBe("all statuses")
    expect(checkboxFilterLabel("show", 4, 2, "statuses")).toBe("2 selected")
    // Hide-model: the count is what survives the deny-list.
    expect(checkboxFilterLabel("hide", 5, 0, "services")).toBe("all services")
    expect(checkboxFilterLabel("hide", 5, 2, "services")).toBe("3 selected")
    expect(checkboxFilterLabel("hide", 5, 5, "services")).toBe("no services")
  })

  it("does not claim 'no services' before any service has been discovered", () => {
    expect(checkboxFilterLabel("hide", 0, 0, "services")).toBe("all services")
  })

  it("never counts below zero when the selection names an item that is not listed", () => {
    // A shared URL can hide a service whose traces have not loaded yet.
    expect(checkboxFilterLabel("hide", 0, 1, "services")).toBe("0 selected")
  })
})

describe("showsTrace", () => {
  const NOTHING_HIDDEN = { hideInternal: false, hiddenServices: new Set<string>() }

  it("hides a trace the server marked internal when 'Hide internal' is ticked", () => {
    // The console polls /_overcast/topology and /_overcast/lambda/instances
    // through the BFF about once a second; both are marked internal by the
    // emulator, and #1613 is what a row of each looks like when they are not.
    expect(
      showsTrace(
        { service: "internal", internal: true },
        { ...NOTHING_HIDDEN, hideInternal: true },
      ),
    ).toBe(false)
  })

  it("shows an internal trace once 'Hide internal' is unticked", () => {
    expect(showsTrace({ service: "internal", internal: true }, NOTHING_HIDDEN)).toBe(true)
  })

  it("keeps a user-facing trace whatever 'Hide internal' says", () => {
    const filter = { ...NOTHING_HIDDEN, hideInternal: true }
    expect(showsTrace({ service: "s3" }, filter)).toBe(true)
    expect(showsTrace({ service: "s3", internal: false }, filter)).toBe(true)
  })

  it("hides a trace whose service is on the dropdown's deny-list", () => {
    const filter = { hideInternal: true, hiddenServices: new Set(["s3"]) }
    expect(showsTrace({ service: "s3" }, filter)).toBe(false)
    expect(showsTrace({ service: "sqs" }, filter)).toBe(true)
  })

  it("matches an unnamed service by the key the dropdown offers for it", () => {
    // The options list coalesces "" to "(unknown)", so the deny-list holds
    // that key and the predicate has to resolve the row the same way.
    expect(serviceKey("")).toBe("(unknown)")
    expect(
      showsTrace({ service: "" }, { hideInternal: true, hiddenServices: new Set(["(unknown)"]) }),
    ).toBe(false)
  })
})
