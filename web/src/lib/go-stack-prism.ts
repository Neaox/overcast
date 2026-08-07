import Prism from "@/lib/prism"

Prism.languages.gostacktrace = {
  "goroutine-header": {
    pattern: /^goroutine \d+ \[[^\]]*\]:$/m,
    inside: {
      keyword: /^goroutine/,
      number: /\b\d+\b/,
      punctuation: /[[\]]/,
    },
  },
  "stack-frame": {
    // github.com/Neaox/overcast/internal/services/sqs.CreateQueue(0x..., 0x...)
    pattern: /^[^\t\n].+[^)]$/m,
    inside: {
      // Package path: github.com/Neaox/overcast/internal/services/
      namespace: {
        pattern: /^([\w.-]+\/)+/,
        inside: {
          punctuation: /\//,
        },
      },
      // Function name: sqs.CreateQueue
      function: {
        pattern: /(?:\.|^)([^.(]+)(?=\()/,
        lookbehind: true,
      },
      // Parenthesized args
      "function-args": {
        pattern: /\([^)]*\)/,
        alias: "attr-value",
      },
      punctuation: /\./,
    },
  },
  "file-frame": {
    pattern: /^\t.+/m,
    inside: {
      // /path/to/file.go
      "file-path": {
        pattern: /^[\t ]+\S+\.\w+/,
        alias: "string",
      },
      // :1234
      "line-number": {
        pattern: /:\d+/,
        alias: "number",
      },
      // +0x1a2b offset
      "hex-offset": {
        pattern: /\+0x[0-9a-f]+/,
        alias: "number",
      },
    },
  },
  "created-by": {
    pattern: /^created by .+$/m,
    inside: {
      keyword: /^created by/,
    },
  },
}

export function highlightGoStack(raw: string): string {
  return Prism.highlight(raw, Prism.languages.gostacktrace, "gostacktrace")
}
