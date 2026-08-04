* [cloudformation] a resource's resolved properties are persisted, so an update can tell which of them changed — every replacement guard, patch diff and custom-resource `OldResourceProperties` compared against nothing before
* [cloudformation/rds] changing a DB instance's `MasterUsername` or `DBName` replaces the instance, as AWS documents, instead of reporting success and leaving it alone
* [cloudformation] dynamic references are compared as written, so rotating a secret behind an unchanged template no longer reads as a changed property
* [cloudformation] stack outputs leave dynamic references literal, as CloudFormation does, rather than publishing the resolved secret through DescribeStacks
* [rds] a DB instance whose container cannot be started reports `failed` with the reason, instead of `available` with nothing behind it
* [rds] starting an instance rebuilds a container Docker no longer has, and the startup sweep keeps containers a stopped instance still owns
* [docker] `IsNotFound` recognises the 404s reported by every client helper, not only the JSON ones
