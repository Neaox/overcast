* [web] the docs navigation is encoded once instead of on every request.
  `/api/docs/nav` re-projected and re-serialised the same 128 KB of JSON per request — ~0.4ms of CPU and ~176 KB of garbage for a body that cannot change until the binary does. It is now built with the search index, behind one `sync.Once`, and served as bytes: a warm request drops from ~360us to ~30us and from 21 allocations to 11
  holding the encoded form also lets the parsed corpus go once the index exists, cutting what a warm docs handler retains from 4.6 MB to 3.5 MB
