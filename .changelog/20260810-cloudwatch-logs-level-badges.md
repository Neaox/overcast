* [web/cloudwatch] the log viewer's level badge now labels every row whose level it
  detects, including a runtime's plain-text `console.*` lines — previously only a
  JSON document was labelled and a text line got the row tint alone
* [web/cloudwatch] level badges are legible in light mode, where the warning badge
  used to be lighter than the row behind it
* [web/cloudwatch] a long log stream name no longer overflows its column into the
  message text in the all-streams view
