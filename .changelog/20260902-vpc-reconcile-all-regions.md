* [ec2] the startup reconcile now covers VPCs in every region, so a VPC outside the default region gets its Docker network back after a restart.
  the default region's pass took every other region's network for an orphan and removed it; a network is now unclaimed only when no VPC in any region names it
  a region the startup pass did not cover is reconciled on the first placement into it, and its networks reach `/_overcast/health` like the rest
