*! [cloudformation/cloudtrail] `AWS::CloudTrail::Trail` now reads the trail name from `TrailName`, the real resource schema's property, instead of `Name`
  migration: rename `Name` to `TrailName` in any template using this resource; a template still setting `Name` gets an auto-generated trail name instead of a validation error, since Overcast has no per-property unrecognised-property diagnostic
