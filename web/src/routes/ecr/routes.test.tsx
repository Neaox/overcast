import { Route as EcrIndexRoute } from "./index"
import { Route as EcrDetailRoute } from "./$repositoryName"

describe("ECR routes", () => {
  it("defines ECR index page title", async () => {
    const head = await EcrIndexRoute.options.head?.({} as never)
    expect(head?.meta?.[0]?.title).toBe("ECR Repositories — Overcast")
  })

  it("defines ECR repository detail page title", async () => {
    const head = await EcrDetailRoute.options.head?.({
      params: { repositoryName: "backend/api" },
    } as never)
    expect(head?.meta?.[0]?.title).toBe("backend/api — ECR — Overcast")
  })
})
