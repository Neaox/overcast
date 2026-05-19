import { createFileRoute } from "@tanstack/react-router"
import { Ec2Dashboard } from "@/features/ec2/components/ec2-dashboard"

export const Route = createFileRoute("/ec2/")({
  head: () => ({ meta: [{ title: "EC2 / VPC — Overcast" }] }),
  component: Ec2Dashboard,
})
