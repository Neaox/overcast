* [lambda] Extensions subscribed to the Telemetry API or the Logs API now receive their records on Docker Desktop.
  Batches are POSTed to the extension's listener by the in-container init, from inside the execution environment, exactly as AWS's platform posts them.
  Previously the emulator posted them from the host at the container's bridge IP, which Docker Desktop's VM-hosted engine has no route back from, so every delivery timed out on Windows and macOS.
