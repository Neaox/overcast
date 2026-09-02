* [networking] a network Overcast creates is now verified afterwards, whichever way it got there.
  a create issued after "no such network" was returned on without a second look, and Docker resolves a name conflict by handing back the existing network *unchanged* — so a network another process created between the two calls was reported as freshly built to this configuration, drift and all. The unreadable-inspect path was fixed in the previous release; this is the same hole reached by the other route
  it costs one inspect per network per start, and finds nothing on the ordinary path
