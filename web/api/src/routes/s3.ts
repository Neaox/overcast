import { Hono } from "hono"
import {
  ListObjectsV2Command,
  DeleteObjectsCommand,
  GetObjectCommand,
  PutObjectCommand,
} from "@aws-sdk/client-s3"
import { resolveEndpoint, ENDPOINT_HEADER, REGION_HEADER } from "../service-discovery.js"
import { AWSClientFactory } from "../client/aws.js"

export const s3Routes = new Hono()

/** Resolve endpoint + return an S3 client from request headers. */
function s3(c: { req: { header: (k: string) => string | undefined } }) {
  const endpoint = resolveEndpoint({
    [ENDPOINT_HEADER]: c.req.header(ENDPOINT_HEADER),
    [REGION_HEADER]: c.req.header(REGION_HEADER),
  })
  return { s3: AWSClientFactory.makeS3(endpoint), endpoint }
}

/**
 * GET /api/s3/buckets/:bucket/objects/:key/download
 *
 * Streams the S3 object body directly to the client — the AWS SDK returns a
 * SdkStream which is a Web ReadableStream, so we pass it straight through
 * to Hono's response with no intermediate buffering.
 */
s3Routes.get("/buckets/:bucket/objects/:key{.+}/download", async (c) => {
  const { s3: client } = s3(c)
  const bucket = c.req.param("bucket")
  const key = decodeURIComponent(c.req.param("key"))

  const res = await client.send(new GetObjectCommand({ Bucket: bucket, Key: key }))

  if (!res.Body) return c.json({ error: "Empty body returned from S3" }, 500)

  // res.Body is an SdkStreamMixin which implements the Web ReadableStream
  // interface — pass it directly, no buffering.
  const stream = res.Body.transformToWebStream()

  const headers = new Headers({
    "Content-Type": res.ContentType ?? "application/octet-stream",
    "Content-Disposition": `attachment; filename="${key.split("/").pop()}"`,
  })
  if (res.ContentLength) headers.set("Content-Length", String(res.ContentLength))
  if (res.ETag) headers.set("ETag", res.ETag)
  if (res.LastModified) headers.set("Last-Modified", res.LastModified.toUTCString())

  return new Response(stream, { status: 200, headers })
})

/**
 * PUT /api/s3/buckets/:bucket/objects/:key
 *
 * Streams the request body directly to S3 — no buffering. Content-Type
 * is taken from the request header.
 */
s3Routes.put("/buckets/:bucket/objects/:key{.+}", async (c) => {
  const { s3: client } = s3(c)
  const bucket = c.req.param("bucket")
  const key = decodeURIComponent(c.req.param("key"))
  const contentType = c.req.header("content-type") ?? "application/octet-stream"

  // Collect x-amz-meta-* headers into the Metadata map.
  const metadata: Record<string, string> = {}
  for (const [k, v] of Object.entries(c.req.raw.headers)) {
    const lower = k.toLowerCase()
    if (lower.startsWith("x-amz-meta-")) {
      metadata[lower.slice("x-amz-meta-".length)] = v as string
    }
  }

  const storageClass = c.req.header("x-amz-storage-class") || undefined
  const contentDisposition = c.req.header("x-object-content-disposition") || undefined
  const contentEncoding = c.req.header("x-object-content-encoding") || undefined
  const contentLanguage = c.req.header("x-object-content-language") || undefined
  const cacheControl = c.req.header("x-object-cache-control") || undefined
  const expires = c.req.header("x-object-expires") || undefined

  // Buffer the body — the AWS SDK cannot calculate a content hash on a
  // ReadableStream, so we must materialise it before sending to PutObjectCommand.
  const body = await c.req.arrayBuffer()

  await client.send(
    new PutObjectCommand({
      Bucket: bucket,
      Key: key,
      Body: new Uint8Array(body),
      ContentType: contentType,
      ...(storageClass && { StorageClass: storageClass as never }),
      ...(Object.keys(metadata).length > 0 && { Metadata: metadata }),
      ...(contentDisposition && { ContentDisposition: contentDisposition }),
      ...(contentEncoding && { ContentEncoding: contentEncoding }),
      ...(contentLanguage && { ContentLanguage: contentLanguage }),
      ...(cacheControl && { CacheControl: cacheControl }),
      ...(expires && { Expires: new Date(expires) }),
    }),
  )

  return c.json({ ok: true }, 200)
})

/**
 * DELETE /api/s3/buckets/:bucket/objects-by-prefix?prefix=...
 *
 * Bulk-deletes all objects whose key begins with the given prefix.
 * Pages through the listing so it works for any number of objects.
 */
s3Routes.delete("/buckets/:bucket/objects-by-prefix", async (c) => {
  const { s3: client } = s3(c)
  const bucket = c.req.param("bucket")
  const prefix = c.req.query("prefix")
  if (!prefix) return c.json({ error: "prefix query parameter is required" }, 400)

  let deleted = 0
  let token: string | undefined
  do {
    const list = await client.send(
      new ListObjectsV2Command({
        Bucket: bucket,
        Prefix: prefix,
        MaxKeys: 1000,
        ContinuationToken: token,
      }),
    )
    const keys = (list.Contents ?? []).map((o) => o.Key).filter(Boolean) as string[]
    if (keys.length > 0) {
      await client.send(
        new DeleteObjectsCommand({
          Bucket: bucket,
          Delete: { Objects: keys.map((Key) => ({ Key })), Quiet: true },
        }),
      )
    }
    deleted += keys.length
    token = list.IsTruncated ? list.NextContinuationToken : undefined
  } while (token)

  return c.json({ ok: true, deleted })
})
