package sns

// handler_stubs.go contains every SNS handler that is not yet implemented.
// Each method returns HTTP 501 Not Implemented with x-emulator-unsupported: true.
//
// Convention: when an operation is implemented, move its method body out of this
// file and into handler.go (or handler_<group>.go for large feature groups).
// handler.go is the authoritative inventory of what actually works.
//
// All operations in this service are now implemented:
//   - Topic operations:        see handler_topic.go
//   - Subscription operations: see handler_subscription.go
//   - Publish operations:      see handler_publish.go
