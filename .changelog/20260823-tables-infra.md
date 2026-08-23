~ [web] CloudFormation, CloudWatch Logs, SSM, Step Functions and Secrets Manager tables sort by
  clicking a column header. On the CloudFormation **Stacks** and CloudWatch **Log Groups** index
  pages the sort is deep-linkable — `?sort=name` / `?sort=-name` — so a sorted view survives a
  reload and can be shared, and Log Groups also gains the **Columns** menu for hiding the ARN.
  Sortable on the detail pages too: a stack's parameters, outputs, tags and resources; a log
  group's streams; an SSM parameter's version history; a state machine's executions; a secret's
  rotation versions. Version history, executions and rotation versions now say newest-first
  rather than trusting the order the API returned them in.
* [web] a log group with no streams, or one whose streams failed to load, says so instead of
  rendering an empty table; the same page's stream list gains the standard skeleton while it
  loads. A secret whose rotation returned no versions no longer renders a headers-only table.
