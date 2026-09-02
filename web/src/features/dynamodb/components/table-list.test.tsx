import { http, HttpResponse } from "msw"
import { renderWithData, screen, waitFor } from "@/test/render"
import { server } from "@/test/server"
import { dynamoTablesQueryOptions } from "@/features/dynamodb/data"
import { TableList } from "./table-list"

vi.mock("@tanstack/react-router", () => ({
  useNavigate: () => vi.fn(),
}))

vi.mock("@/features/docs/service-docs-modal", () => ({
  ServiceDocsButton: () => null,
  useDocsFromHash: () => [false, vi.fn(), vi.fn()],
}))

vi.mock("@/features/debug/raw-state-link", () => ({
  RawStateLink: () => null,
}))

vi.mock("./create-table-dialog", () => ({
  CreateTableDialog: () => null,
}))

vi.mock("@/hooks/use-resource-mutation", () => ({
  useResourceMutation: () => ({ mutate: vi.fn(), isPending: false }),
}))

function seed(tables: Record<string, unknown>[]) {
  return [[dynamoTablesQueryOptions().queryKey, tables]] as [readonly unknown[], unknown][]
}

const table = {
  tableName: "orders",
  tableStatus: "ACTIVE",
  tableArn: "arn:aws:dynamodb:us-east-1:000000000000:table/orders",
  itemCount: 0,
  tableSizeBytes: 0,
  keySchema: [{ attributeName: "id", keyType: "HASH" }],
  attributeDefinitions: [],
}

// The report this closes: the CLI created tables in the region AWS_REGION
// names while the console listed the region it defaults to, and the page
// read as "ListTables returns []". The empty state is the one place that
// sentence is worth saying.
describe("TableList — region preflight", () => {
  it("explains an empty list when the tables are in another region", async () => {
    server.use(
      http.get("/api/preflight/region", () =>
        HttpResponse.json({
          kind: "dynamodb-tables",
          region: "us-east-1",
          count: 0,
          elsewhere: [{ region: "ap-southeast-2", count: 2 }],
        }),
      ),
    )

    renderWithData(<TableList />, seed([]))

    expect(await screen.findByText(/No tables in/)).toHaveTextContent(
      "No tables in us-east-1. There are 2 in ap-southeast-2.",
    )
  })

  it("says nothing about regions when there is nothing anywhere", async () => {
    let asked = 0
    server.use(
      http.get("/api/preflight/region", () => {
        asked++
        return HttpResponse.json({
          kind: "dynamodb-tables",
          region: "us-east-1",
          count: 0,
          elsewhere: [],
        })
      }),
    )

    renderWithData(<TableList />, seed([]))

    expect(await screen.findByText("No tables yet")).toBeInTheDocument()
    await waitFor(() => expect(asked).toBe(1))
    expect(screen.queryByText(/No tables in/)).not.toBeInTheDocument()
  })

  it("does no cross-region work when the list is not empty", () => {
    let asked = 0
    server.use(
      http.get("/api/preflight/region", () => {
        asked++
        return HttpResponse.json({
          kind: "dynamodb-tables",
          region: "us-east-1",
          count: 0,
          elsewhere: [{ region: "ap-southeast-2", count: 2 }],
        })
      }),
    )

    renderWithData(<TableList />, seed([table]))

    expect(screen.getByText("orders")).toBeInTheDocument()
    expect(asked).toBe(0)
  })
})
