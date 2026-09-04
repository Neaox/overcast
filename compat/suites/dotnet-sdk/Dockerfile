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
# Sources only: a developer's Tests/bin and Tests/obj would otherwise come along
# with the directory and carry host paths into the build.
COPY dotnet-sdk/Tests/*.cs ./Tests/
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
COPY registry.json /registry.json
COPY registry.generated.json /registry.generated.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["dotnet", "OvercastCompat.dll"]
