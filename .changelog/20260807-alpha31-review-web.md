* [web] path-style S3 copy URLs percent-encode object keys, so keys with `#`, `?`, spaces, or unicode paste as working links
* [web] bundled builds follow the server-injected API endpoint on boot instead of a stale stored one; only endpoints entered in the connection dialog persist as overrides
* [web] reopening connection settings seeds the form from the active endpoint, so Connect keeps a custom endpoint instead of reverting it to the default
