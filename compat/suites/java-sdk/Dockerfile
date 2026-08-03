# ── Build stage ──────────────────────────────────────────────────────────────
# Build context must be compat/suites/ (parent of java-sdk/) so that the
# shared registry.json can be copied in alongside the Java sources.
FROM maven:3.9-eclipse-temurin-17-alpine AS build

WORKDIR /build
COPY java-sdk/pom.xml .
# Pre-fetch dependencies so subsequent builds are cached
RUN mvn dependency:go-offline -q

COPY java-sdk/src/ src/
# The registry loader's unit tests resolve the suite's impl keys against the
# real registry.json, which WORKDIR /build finds at ../registry.json. Running
# them here means an unusable registration fails the image build rather than
# surfacing as a wrong result in a compat run.
COPY registry.json /registry.json
RUN mvn package -q

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
