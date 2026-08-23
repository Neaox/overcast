package containerlogs

// Observer is told where a Follower's pipeline stands: whether the daemon has
// answered, whether a stream is open, whether the reader is blocked in a live
// Read or between two of them, and how many assembled lines are still on their
// way to the sink.
//
// # What it is for
//
// It has one caller, and it exists for one reason. Lambda's `X-Amz-Log-Result`
// tail and its teardown drain have to know when an invocation's output has
// finished arriving, and Docker's log stream cannot tell them: it has no
// per-request boundaries, so "finished" can only be inferred from the pipeline
// going quiet. Inferring it well needs to distinguish the states this interface
// reports — a daemon that has not answered the first connect yet from one that
// refused it, a reader blocked in a live Read from one backing off between
// connections, a line parsed but not yet written from no line at all. Getting
// that distinction wrong is what four rounds of fixes to that wait were about
// (#873, #1160, #1325, #1402).
//
// # Contract
//
// Every method runs on the follower's read path — one ReadStarted/ReadReturned
// pair per Read, one LineScanned per line — so implementations must be cheap
// and must not block. LineScanned and LinesRetired are a matched pair: every
// line that is scanned is retired exactly once, whether it was dropped as a
// duplicate, refused by the sink, or written by a Flush. Stream events come
// from the follower's own goroutine, read and line events from its reader
// goroutine, so an implementation needs its own synchronisation (Lambda's is a
// set of atomics).
//
// # Its future
//
// It is transitional. docs/plans/lambda-in-container-init.md replaces the whole
// inference with a protocol — an init process inside the container that owns
// the runtime's stdout and tells Overcast where each invocation's output ends.
// That plan's Phase 2 deletes Lambda's waits, and Phase 3 deletes this
// interface with them. Nothing new should grow a use for it: a sink that needs
// to know a line arrived has LineSink.
type Observer interface {
	// StreamAnswered reports that the daemon has answered a follow request —
	// with a stream, or with an error. Either way the question is no longer
	// outstanding, which is what separates a first connect still in flight from
	// a follower backing off from an answer it already has.
	StreamAnswered()
	// StreamOpened reports that a stream is open and the reader has not yet
	// entered its first Read on it.
	StreamOpened()
	// StreamClosed reports that the stream is gone. Nothing can arrive until
	// another opens.
	StreamClosed()
	// ReadStarted reports that the reader is about to block in a Read on the
	// open stream. From here until ReadReturned, silence from the daemon is
	// something it has been asked about.
	ReadStarted()
	// ReadReturned reports that the Read returned n bytes; n may be 0.
	ReadReturned(n int)
	// LineScanned reports that a complete line has been assembled and is now in
	// flight towards the sink.
	LineScanned()
	// LinesRetired reports that n in-flight lines have left the pipeline —
	// dropped as duplicates or empty, refused by the sink, or delivered by a
	// Flush.
	LinesRetired(n int)
}

// nopObserver is what a follower with no Observer uses, so the read path needs
// no nil check.
type nopObserver struct{}

func (nopObserver) StreamAnswered()  {}
func (nopObserver) StreamOpened()    {}
func (nopObserver) StreamClosed()    {}
func (nopObserver) ReadStarted()     {}
func (nopObserver) ReadReturned(int) {}
func (nopObserver) LineScanned()     {}
func (nopObserver) LinesRetired(int) {}
