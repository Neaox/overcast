FROM mcr.microsoft.com/dotnet/sdk:8.0-alpine AS build

WORKDIR /build
COPY dotnet-sdk/OvercastCompat.csproj ./
RUN dotnet restore OvercastCompat.csproj

COPY dotnet-sdk/*.cs ./
COPY dotnet-sdk/Harness/ ./Harness/
COPY dotnet-sdk/Clients/ ./Clients/
COPY dotnet-sdk/Groups/ ./Groups/
COPY dotnet-sdk/Registry/ ./Registry/
RUN dotnet publish OvercastCompat.csproj -c Release -o /out --no-restore

FROM mcr.microsoft.com/dotnet/runtime:8.0-alpine

WORKDIR /app
COPY --from=build /out/ ./
COPY registry.json /registry.json
ENV OVERCAST_REGISTRY_PATH=/registry.json
ENV OVERCAST_ENDPOINT=http://localhost:4566
ENV OVERCAST_DEFAULT_REGION=us-east-1

ENTRYPOINT ["dotnet", "OvercastCompat.dll"]
