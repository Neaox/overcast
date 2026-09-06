*! [autoscaling] a group naming neither `AvailabilityZones` nor `VPCZoneIdentifier` is refused with AWS's `ValidationError`
  marked breaking because it is stricter validation: such a group used to be stored and launched nowhere in particular, falling back to `<region>a`
  `UpdateAutoScalingGroup` applies the same rule to the group the update would leave, so an update cannot strip a group of both
  migration: give every group `--availability-zones` or `--vpc-zone-identifier`; a group already stored without either accepts an update that supplies one
