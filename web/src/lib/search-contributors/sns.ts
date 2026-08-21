import { sns } from "@/services/api"
import { snsKeys } from "@/features/sns/data"
import type { SNSTopic } from "@/types"
import { createSearchContributor } from "./create-contributor"

createSearchContributor<SNSTopic>({
  id: "sns",
  // Reuse the feature's own key factory so this can never drift out of sync
  // with the shape (and endpoint/region scoping) the real query uses.
  cacheKey: () => snsKeys.topics(),
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
