~ [docs] Correct the networking, HTTPS, performance and storage guides against the code, and clear filler wording across all twenty pages
  Host-routed addressing now lists the `elb` label and `localhost.floci.io`, both of which route in code but were missing from the tables.
  Egress modes now says which variables reach a Lambda container and which reach an ECS task, instead of claiming all of them reach both.
  Dropped an unsubstantiated startup measurement from Performance, and corrected the ECR `repositoryUri` exception on the URL page.
