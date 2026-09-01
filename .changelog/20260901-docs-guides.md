~ [docs] every non-service guide reviewed and tightened to the content charter
  docs/persistence.md merged into docs/storage.md, and
  docs/multi-container-networking.md folded into docs/networking.md, so the
  storage backends and the Docker Compose hostname question are each documented
  in one place
  docs/README.md routes by task, troubleshooting.md opens with a symptom index
  spanning every guide, and the root README is a front door again rather than a
  second CLI reference
* [docs] configuration reference was missing OVERCAST_SERVICE_METRICS and OVERCAST_UI_PORT, and the operation manifest was several hundred registrations out of date
* [docs] wrong or unresolvable references: a cdk watch example on port 2456, a claim that OVERCAST_SIGV4_VALIDATE was not implemented yet, stale service and resource-type counts, and links into CONTRIBUTING.md, AGENTS.md and docs/dev/ that the published site cannot open
