/**
 * `/s3/<bucket>` is the bucket's front door, and every link into a bucket
 * still points here. The browser itself lives one segment down so that the
 * folder or object it is showing can be part of the path, so this route only
 * forwards — replacing rather than pushing, to keep a redirect out of the
 * user's Back button.
 */
import { createFileRoute, redirect } from "@tanstack/react-router"

export const Route = createFileRoute("/s3/$bucket/")({
  beforeLoad: ({ params }) => {
    // eslint-disable-next-line @typescript-eslint/only-throw-error -- TanStack Router convention
    throw redirect({
      to: "/s3/$bucket/objects/$",
      params: { bucket: params.bucket, _splat: "" },
      replace: true,
    })
  },
  component: () => null,
})
