*! [transfer] `DescribeServer` no longer returns a `CreatedAt` member on the described server — the AWS Transfer Family API has no server creation timestamp anywhere in its model, so the emulator was inventing wire data
  migration: stop reading `Server.CreatedAt` from raw DescribeServer responses; the AWS SDKs never surfaced the member, so SDK-based clients are unaffected
