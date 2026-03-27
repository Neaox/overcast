import { createFileRoute } from "@tanstack/react-router"

export const Route = createFileRoute("/s3/$bucket/config")({
  component: RouteComponent,
})

function RouteComponent() {
  return <div>Hello "/s3/$bucket/config"!</div>
}
