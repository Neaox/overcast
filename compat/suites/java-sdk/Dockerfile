# ── Build stage ──────────────────────────────────────────────────────────────
# Build context must be compat/suites/ (parent of java-sdk/) so that the
# shared registry.json can be copied in alongside the Java sources.
FROM maven:3.9-eclipse-temurin-17-alpine AS build

WORKDIR /build
COPY java-sdk/pom.xml .
# Pre-fetch dependencies so subsequent builds are cached.
#
# This step is the suite's one long conversation with Maven Central, and a
# Central blip used to fail it three runner-level retries in a row (10 s and
# 20 s apart — 2026-08-24, run 32675186656, where `-q` also swallowed the
# reason). Two changes, both aimed at that failure: the fetch retries here,
# inside the layer, with a backoff long enough to outlive a real blip
# (15+30+45+60 s across five attempts); and --no-transfer-progress replaces
# -q, which keeps the noise down without silencing errors.
RUN for i in 1 2 3 4 5; do         mvn dependency:go-offline --no-transfer-progress && break;         if [ "$i" = 5 ]; then echo "mvn dependency:go-offline failed after 5 attempts" >&2; exit 1; fi;         echo "mvn dependency:go-offline failed (attempt $i/5), retrying in $((i*15))s..." >&2;         sleep $((i*15));     done

COPY java-sdk/src/ src/
# The registry loader's unit tests resolve the suite's impl keys against the
# real registry.json, which WORKDIR /build finds at ../registry.json. Running
# them here means an unusable registration fails the image build rather than
# surfacing as a wrong result in a compat run.
COPY registry.json /registry.json
# Loud on failure, quiet on progress; not retried — after go-offline, a
# package failure is a compile or test failure, and retrying those only
# delays the report.
RUN mvn package --no-transfer-progress

# ── Runtime stage ─────────────────────────────────────────────────────────────
FROM eclipse-temurin:17-jre-alpine

WORKDIR /app
COPY --from=build /build/target/java-sdk-compat-*.jar app.jar
# Embed the shared service registry so the image is self-contained.
COPY registry.json /registry.json
ENV OVERCAST_REGISTRY_PATH=/registry.json

ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

CMD ["java", "-jar", "app.jar"]
