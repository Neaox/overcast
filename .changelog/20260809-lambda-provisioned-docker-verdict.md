* [lambda] `PutProvisionedConcurrencyConfig` issued while Overcast is still
  probing Docker now waits for the answer instead of reporting `FAILED` with
  "Docker is not available, so no execution environments can be allocated." The
  pool is found by scanning the runtime registry, and before the probe reports
  the only runtime registered is the stub — so a reservation made in the first
  moments of a process's life was declared impossible on a machine where Docker
  was running fine, and never allocated. `GetProvisionedConcurrencyConfig`,
  `ListProvisionedConcurrencyConfigs` and `DeleteProvisionedConcurrencyConfig`
  read the pool the same way and are fixed with it.
