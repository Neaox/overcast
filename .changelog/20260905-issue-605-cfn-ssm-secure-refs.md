*! [cloudformation] `{{resolve:ssm-secure:...}}` resolves only in the properties AWS allows secure strings in (#605)
  a reference anywhere else fails the resource and rolls the stack back, naming the reference and the property path; the parameter is never decrypted
  `secretsmanager` and plain `ssm` references stay usable in any property
  migration: move an `ssm-secure` reference outside AWS's enumerated properties to `secretsmanager`, or to a stack parameter
