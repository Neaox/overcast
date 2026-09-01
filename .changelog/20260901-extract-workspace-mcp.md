-. [cli] the dev-only `overcast mcp` workspace-MCP subcommand — never shipped in a release.
  the workspace MCP server (repo-aware tools for agents/editors) is now its own standalone command, `cmd/overcast-mcp`, run with `go run ./cmd/overcast-mcp --stdio` instead of `overcast mcp --stdio`
