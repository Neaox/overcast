~ [server] cleartext HTTP/2 (h2c) is served through the standard library's `http.Server.Protocols`
  instead of the deprecated `x/net/http2/h2c` wrapper; prior-knowledge HTTP/2 and HTTP/1.1 behave
  as before, and the rarely used `Upgrade: h2c` handshake now continues on HTTP/1.1
