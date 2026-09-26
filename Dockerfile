# One image for the three roles (collector, engine, api) and the operator subcommands; the role is
# the first argument, as with the binary. Migrations are embedded in the binary; platform packs are
# shipped next to it at /packs, which is where --packs looks by default (the working directory is /).
FROM --platform=$BUILDPLATFORM golang:1.27.1 AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /out/netmapper ./cmd/netmapper

# distroless static: no shell, no package manager, CA certificates included, runs as a non-root user.
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/netmapper /netmapper
COPY packs /packs
WORKDIR /
ENTRYPOINT ["/netmapper"]
