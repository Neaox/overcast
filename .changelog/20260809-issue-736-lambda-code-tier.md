* [state] Lambda deployment packages now carry an explicit storage-tier
  classification, pinning them to the SQLite-backed tier so a future change
  cannot silently make every stored zip memory-resident at startup
