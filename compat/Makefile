# compat/Makefile
#
# Convenience targets for running the compat suite.
# All targets work cross-platform via Docker; no local toolchain required.
#
# Usage:
#   make -C compat          # same as: make -C compat run
#   make -C compat run
#   make -C compat run SUITE=node-js-sdk
#   make -C compat json
#   make -C compat ci

COMPOSE = docker compose -f docker-compose.yml

.PHONY: run json ci build clean dev ui baseline-check baseline-update baseline-lint

## dev — build UI + binary, start Overcast if needed, open dashboard in browser
dev:
	@bash dev.sh

## ui — (re)build the compat UI only
ui:
	cd ui && npm install --silent && npm run build --silent

## run — pretty-printed results (default)
run:
	$(COMPOSE) run --rm -e OVERCAST_COMPAT_SUITE="$(SUITE)" compat

## json — NDJSON output to stdout (useful for CI artefacts / dashboards)
json:
	$(COMPOSE) run --rm -e OVERCAST_COMPAT_FORMAT=json -e OVERCAST_COMPAT_SUITE="$(SUITE)" compat

## ci — same as run but also rebuilds images first (for use in pipelines)
ci:
	$(COMPOSE) build --pull
	$(COMPOSE) run --rm -e OVERCAST_COMPAT_SUITE="$(SUITE)" compat

## build — pre-build all images without running
build:
	$(COMPOSE) build

## clean — remove stopped containers and local images
clean:
	$(COMPOSE) down --rmi local --volumes --remove-orphans

## report — print an agent-friendly summary of the last compat run (reads compat-results.json)
report:
	go run ../cmd/compat --report

## baseline-check — fail if compat-results.json regresses against compat/baseline.json
baseline-check:
	go run ../cmd/compat --compare-baseline --baseline-file baseline.json --results-file ../compat-results.json

## baseline-update — update compat/baseline.json with improvements from compat-results.json
baseline-update:
	go run ../cmd/compat --update-baseline --baseline-file baseline.json --results-file ../compat-results.json

## baseline-lint — reject baseline downgrades; set OLD_BASELINE to compare against a non-default file
baseline-lint:
	@if [ -z "$(OLD_BASELINE)" ]; then echo "OLD_BASELINE is required" >&2; exit 2; fi
	go run ../cmd/compat --lint-baseline-from "$(OLD_BASELINE)" --lint-baseline-to baseline.json
