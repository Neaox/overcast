* [rds] Deleting an Aurora DB instance now removes it from its cluster, and deleting the writer
  promotes a survivor. `DBClusterMembers` was only ever appended to, so `DeleteDBInstance` dropped
  the instance record and left the membership entry behind: `DescribeDBClusters` went on reporting a
  member that no longer existed, and if the deleted member was the writer the cluster was left with
  no writer at all — which took its endpoints with it, since the cluster's address, port and DNS all
  answer from whichever member holds that role. A surviving member is now promoted the way AWS
  chooses one during a failover, by promotion tier, and the cluster endpoint names move onto the new
  writer's engine container. Moving them detaches and re-attaches that container, so connections
  held open to it over that network drop, exactly as a failover on AWS drops them. A cluster whose
  last member is deleted keeps its AWS-shaped endpoint names and its own port, unchanged.
