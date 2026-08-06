* [ecs/rds/elasticache/msk] resources no longer report RUNNING/available/ACTIVE when Docker is unavailable
~ [web] Docker connectivity is now visible on the /metrics health page and via info banners on Docker-backed resource pages
+ [router] GET /_health now includes a `docker` block with per-service Docker connection status Responses from Docker-backed describe endpoints carry `x-overcast-backing`, `x-overcast-backing-reason`, and `x-overcast-container-health` headers
