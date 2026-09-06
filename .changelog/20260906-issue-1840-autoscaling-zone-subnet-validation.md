*! [autoscaling] a group whose `VPCZoneIdentifier` subnets are not in its `AvailabilityZones` is refused with AWS's `ValidationError`
  marked breaking because it is stricter validation: the pair used to be stored unchecked and round-robinned independently, so a launch could land in a zone its own subnet contradicted
  given only `VPCZoneIdentifier`, the group takes its zones from the subnets, as AWS does
  each launch goes to a subnet and lets EC2 place it in that subnet's own zone, so the two can no longer disagree
  migration: drop `AvailabilityZones` and let the subnets set the zones, or list exactly the zones those subnets are in
* [cloudformation/autoscaling] `AWS::AutoScaling::AutoScalingGroup` reads `VPCZoneIdentifier` in its documented list form, not only as a string
