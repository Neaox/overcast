/// <reference lib="webworker" />

import { buildLayoutNodes } from './map-layout'

self.onmessage = (e: MessageEvent) => {
  const { id, topologyNodes, topologyEdges, nodeSizeOverrides, activeRegion, collapsedStacks } = e.data
  try {
    const result = buildLayoutNodes(
      topologyNodes,
      topologyEdges,
      nodeSizeOverrides ?? {},
      activeRegion,
      new Set(collapsedStacks ?? []),
    )
    self.postMessage({ id, nodes: result, error: null })
  } catch (err) {
    self.postMessage({ id, nodes: null, error: String(err) })
  }
}
