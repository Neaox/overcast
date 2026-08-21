* [ecs] `StopTask` over RPC v2 CBOR records the stop before it kills the
  containers, as the JSON path already did. Killing them first let the Docker
  die event file the task as `EssentialContainerExited` instead of
  `UserInitiated`, charge its deployment a failed task, and schedule a service
  replacement nobody asked for. The two paths now share one implementation, so
  the CBOR one also takes a stopped task out of its service's target groups.
