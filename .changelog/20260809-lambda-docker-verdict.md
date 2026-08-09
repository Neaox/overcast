* [lambda] an invocation that arrives while Overcast is still probing Docker now
  waits for the answer instead of being told Docker is unavailable. The stub
  runtime is registered synchronously at startup and the container runtime
  arrives on a background goroutine, so a function created and invoked in the
  first moments of a process's life was answered `Runtime.DockerUnavailable` on
  a machine where Docker was running fine.
* [lambda] Docker that was not running when Overcast started is now picked up
  automatically. The startup probe's verdict used to be latched for the life of
  the process, so starting the emulator before Docker Desktop had finished
  booting left Lambda permanently unable to invoke — and the error told the user
  to restart the emulator, which was the only recovery there was.
