* [web] The deploy-diagnostics tab no longer crashes for a stack whose failed resource is not an ECS service
  The server omits a resource's evidence sections when no collector covers its type, which is every type but AWS::ECS::Service.
  The console's hand-written type declared the field required, so nothing caught the case until the page threw on it.
