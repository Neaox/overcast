* [cloudformation] generated Kinesis stream, IAM policy, managed policy, instance profile, group and Lambda layer names are capped at their services' limits.
  they were minted against the 255-character default, so a long stack name and logical ID produced a name the real service rejects — the defect #1691 fixed for buckets, queues, topics and roles.
  IAM allows a group, policy, managed policy or instance profile 128 characters and a role or user 64; one constant carried 64 for all of them, truncating the first four needlessly.
