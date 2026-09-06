# Build context must be compat/suites/ (parent of dotnet-sdk/) so that the
# shared registry.json can be copied in alongside the C# sources — the same
# convention java-sdk and rust-sdk use. The shared error-fixture corpus
# (compat/model/testdata/errors) is not reachable from this context; see
# compat/AGENTS.md § Where the shared error corpus runs. ScenarioErrorFixtureTests
# detects that and skips itself here rather than failing the build — it runs
# for real in test.yml's compat-suite-unit-tests job, from a full checkout.
FROM mcr.microsoft.com/dotnet/sdk:8.0-alpine AS build

WORKDIR /build
COPY dotnet-sdk/OvercastCompat.csproj ./
COPY dotnet-sdk/Tests/OvercastCompat.Tests.csproj ./Tests/
RUN dotnet restore OvercastCompat.csproj && dotnet restore Tests/OvercastCompat.Tests.csproj

COPY dotnet-sdk/*.cs ./
COPY dotnet-sdk/Harness/ ./Harness/
COPY dotnet-sdk/Clients/ ./Clients/
COPY dotnet-sdk/Groups/ ./Groups/
COPY dotnet-sdk/Registry/ ./Registry/
COPY dotnet-sdk/Scenario/ ./Scenario/
# Sources only: a developer's Tests/bin and Tests/obj would otherwise come along
# with the directory and carry host paths into the build.
COPY dotnet-sdk/Tests/*.cs ./Tests/
# RegistrationTests (#1700) resolves the suite's real registrations against the
# real registry.json, so it has to be present for the test run below — the
# runtime stage below copies its own for the same reason, one directory later.
COPY registry.json ./
COPY registry.generated.json ./
# Running the registry loader's unit tests here means a loader regression fails
# the image build rather than surfacing as a wrong result in a compat run — the
# guard `mvn package` already gives the java-sdk suite. ScenarioErrorFixtureTests
# comes along too, but the corpus it needs is not in this build context (see
# above), so it skips itself and the build stays green.
RUN dotnet test Tests/OvercastCompat.Tests.csproj -c Release --no-restore
RUN dotnet publish OvercastCompat.csproj -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/runtime:8.0-alpine

WORKDIR /app
COPY --from=build /out/ ./
# The generated sibling comes along because the loader reads it from the same
# directory. Its absence is not an error — it reads as an empty registry — so an
# image that dropped it would silently run none of the generated groups.
COPY registry.json /registry.json
COPY registry.generated.json /registry.generated.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["dotnet", "OvercastCompat.dll"]
