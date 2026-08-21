*! [config] native `overcast serve` now defaults to binding `127.0.0.1` instead of `0.0.0.0`; the containerised default (Docker image, `OVERCAST_DATA_DIR_SOURCE=image`) is unchanged at `0.0.0.0`. An explicit `OVERCAST_LISTEN` always wins over either default
  migration: native users reaching Overcast from another machine, a VM, or a phone on the same network must set `OVERCAST_LISTEN=0.0.0.0` to restore the old reach
+ [config] the startup bind-address log line now also says *why* a default was chosen — containerised or native
