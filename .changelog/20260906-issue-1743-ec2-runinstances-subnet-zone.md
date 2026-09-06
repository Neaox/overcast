* [ec2] `RunInstances` places an instance in the availability zone of the `SubnetId` it launches into
  the zone fell back to `<region>a` whatever zone the subnet was in, so an instance reported a placement its own `subnetId` contradicted (#1743)
  a `Placement.AvailabilityZone` that disagrees with the subnet's zone is now refused with `InvalidParameterValue`, as EC2 does
