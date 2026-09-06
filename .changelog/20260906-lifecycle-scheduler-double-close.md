* [lifecycle] Cancelling a delayed state transition at the moment it fires no longer crashes the process
  Cancel, Stop and a rescheduled key all read the timer's own report of having been stopped as proof its callback would not run, so both sides could finish the same transition.
  The second close of its completion channel then panicked. Ownership is now claimed once, under the scheduler's lock, and a transition a cancel wins does not run its callback at all.
