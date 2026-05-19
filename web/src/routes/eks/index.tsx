import { createFileRoute } from "@tanstack/react-router"
import { EksPage } from "@/features/eks/components/eks-page"

export const Route = createFileRoute("/eks/")({
  head: () => ({ meta: [{ title: "EKS Clusters — Overcast" }] }),
  component: EksPage,
})
