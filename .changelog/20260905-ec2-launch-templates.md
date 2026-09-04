+ [ec2/autoscaling/cloudformation] EC2 launch templates, and Auto Scaling groups that launch from them
  CDK's `autoscaling.AutoScalingGroup` emits a launch template by default, so a CDK ASG stack now deploys instead of hitting a 501.
  Seven EC2 operations, `lt-` IDs, versions from 1 with `$Latest`/`$Default`, and `RunInstances` merging a template under explicit parameters.
  `AWS::EC2::LaunchTemplate` provisions for real: `Ref` is the template ID, and a data change makes a new default version rather than replacing.
