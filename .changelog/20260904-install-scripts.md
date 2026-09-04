+ [cli] one-line installers: `curl -fsSL https://overcast.sh/install.sh | sh` and `irm https://overcast.sh/install.ps1 | iex`
  detects OS and CPU, verifies the download against SHA256SUMS, installs to a per-user directory without sudo; flags and variables in docs/install.md
  each release also carries a copy of both scripts pinned to that release, and CI installs from the published assets on Linux, macOS and Windows
