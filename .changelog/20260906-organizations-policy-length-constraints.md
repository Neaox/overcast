* [organizations] over-length policy `Name`/`Description` now return `InvalidInputException` (`MAX_LENGTH_EXCEEDED`)
  `CreatePolicy`/`UpdatePolicy` enforce the modeled 128/512 character limits instead of accepting any length
