* [web] "Hide internal" on the traces page now hides the console's own topology and Lambda-instance polling.
  the emulator classified `/_overcast/topology` and `/_overcast/lambda/instances` as user traffic, so a row of each appeared roughly once a second.
  a deep-search match is filtered the same way as a listed row, which it was not before — a hidden service reappeared as soon as a search matched a body.
