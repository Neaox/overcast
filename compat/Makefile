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

.PHONY: run json ci build clean dev serve ui baseline-check failures-check baseline-update baseline-lint baseline-size

## dev — dashboard with a hot-reloading UI, on free ports, opened in a browser
dev:
	go run ../cmd/compat --dev

## serve — dashboard with a pre-built UI, on free ports, opened in a browser
serve:
	go run ../cmd/compat --serve --interactive --build-ui --open

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

## baseline-check — fail if compat-results.json regresses against compat/baseline/
baseline-check:
	go run ../cmd/compat --compare-baseline --baseline-file baseline --results-file ../compat-results.json

## failures-check — fail if compat-results.json holds any failing test, baseline or no baseline
failures-check:
	go run ../cmd/compat --max-failures 0 --results-file ../compat-results.json

## baseline-update — update compat/baseline/ with improvements from compat-results.json
baseline-update:
	go run ../cmd/compat --update-baseline --baseline-file baseline --results-file ../compat-results.json

## baseline-lint — reject baseline downgrades; OLD_BASELINE is the baseline to compare against (a shard directory or a single file)
# The registry paths are relative to this directory, and are the working tree's:
# they say whether a removed expectation is one a suite is still asked to
# produce, so they must describe the change being linted, not the old baseline.
baseline-lint:
	@if [ -z "$(OLD_BASELINE)" ]; then echo "OLD_BASELINE is required" >&2; exit 2; fi
	go run ../cmd/compat --lint-baseline-from "$(OLD_BASELINE)" --lint-baseline-to baseline \
		--registry-file suites/registry.json --generated-registry-file suites/registry.generated.json

## baseline-size — fail if any per-suite baseline shard has outgrown its size budget
baseline-size:
	go run ../cmd/compat --lint-baseline-size --baseline-file baseline
