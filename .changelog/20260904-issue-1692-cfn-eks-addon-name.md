*! [eks/cloudformation] CreateAddon and AWS::EKS::Addon refuse an addonName DescribeAddonVersions does not publish.
  Ref on AWS::EKS::Addon now returns "<cluster>|<addon>", as on AWS.
  migration: use a published add-on name (vpc-cni, coredns, kube-proxy, aws-ebs-csi-driver, eks-pod-identity-agent) or add the add-on to the emulated catalog.
