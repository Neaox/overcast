FROM mcr.microsoft.com/dotnet/sdk:8.0-alpine AS build

WORKDIR /build
COPY suites/dotnet-sdk/OvercastCompat.csproj ./
COPY suites/dotnet-sdk/Tests/OvercastCompat.Tests.csproj ./Tests/
RUN dotnet restore OvercastCompat.csproj && dotnet restore Tests/OvercastCompat.Tests.csproj

COPY suites/dotnet-sdk/*.cs ./
COPY suites/dotnet-sdk/Harness/ ./Harness/
COPY suites/dotnet-sdk/Clients/ ./Clients/
COPY suites/dotnet-sdk/Groups/ ./Groups/
COPY suites/dotnet-sdk/Registry/ ./Registry/
COPY suites/dotnet-sdk/Scenario/ ./Scenario/
# Sources only: a developer's Tests/bin and Tests/obj would otherwise come along
# with the directory and carry host paths into the build.
COPY suites/dotnet-sdk/Tests/*.cs ./Tests/
# RegistrationTests (#1700) resolves the suite's real registrations against the
# real registry.json, so it has to be present for the test run below — the
# runtime stage below copies its own for the same reason, one directory later.
COPY suites/registry.json ./
COPY suites/registry.generated.json ./
# The shared error-matching conformance fixtures, at the repository-relative
# path the test resolves them by — walking up from its own output directory,
# the same rule RegistrationTests resolves registry.json by. Every backend has
# to answer these identically, so the suite that cannot see them is the suite
# that silently stops checking.
COPY model/testdata/errors/ ./compat/model/testdata/errors/
# Running the registry loader's unit tests here means a loader regression fails
# the image build rather than surfacing as a wrong result in a compat run — the
# guard `mvn package` already gives the java-sdk suite.
RUN dotnet test Tests/OvercastCompat.Tests.csproj -c Release --no-restore
RUN dotnet publish OvercastCompat.csproj -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/runtime:8.0-alpine

WORKDIR /app
COPY --from=build /out/ ./
# The generated sibling comes along because the loader reads it from the same
# directory. Its absence is not an error — it reads as an empty registry — so an
# image that dropped it would silently run none of the generated groups.
COPY suites/registry.json /registry.json
COPY suites/registry.generated.json /registry.generated.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["dotnet", "OvercastCompat.dll"]
