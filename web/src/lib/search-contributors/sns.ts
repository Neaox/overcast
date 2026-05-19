import { sns } from "@/services/api"
import type { SNSTopic } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SNSTopic>({
  id: "sns",
  cacheKey: (ep) => ["sns", "topics", ep.baseUrl],
  fetchAll: () => sns.listTopics(),
  matchFields: (t) => [t.TopicArn?.split(":").pop() ?? "", t.TopicArn ?? ""],
  toResult: (t) => {
    const name = t.TopicArn?.split(":").pop() ?? ""
    return {
      id: `sns:${name}`,
      label: name,
      sublabel: t.TopicArn ?? "",
      service: "SNS",
      serviceKey: "/sns",
      type: name.endsWith(".fifo") ? "FIFO Topic" : "Topic",
      href: `/sns/${encodeURIComponent(name)}`,
    }
  },
})
