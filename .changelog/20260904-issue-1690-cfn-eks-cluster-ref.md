* [cloudformation] `Ref` on `AWS::EKS::Cluster` now returns the cluster name, as on AWS, instead of the ARN.
  a nodegroup, Fargate profile or add-on wired to its cluster by `Ref` now reaches CREATE_COMPLETE.
  `Fn::GetAtt Cluster.Arn` still returns the ARN; stacks stored before this fix still delete cleanly.
