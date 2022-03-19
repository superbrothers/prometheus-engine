# syntax = docker/dockerfile:1
FROM --platform=${BUILDPLATFORM} golang:1.17-buster AS buildbase

# Compile the UI assets.
FROM --platform=${BUILDPLATFORM} launcher.gcr.io/google/nodejs as assets
# To build the UI we need a recent node version and the go toolchain.
RUN install_node v14.16.1
COPY --from=buildbase /usr/local/go /usr/local/
ENV PATH="/usr/local/go/bin:${PATH}"
COPY . /app
RUN /app/pkg/ui/build.sh

# sync is used to copy all auto-generated files to a different context.
# Usually this is used to mirror the changes back to the host machine.
FROM scratch as sync
COPY --from=assets /app/pkg/ui/assets_vfsdata.go pkg/ui/assets_vfsdata.go

# Build the actual Go binary.
FROM buildbase AS appbase
WORKDIR /app
COPY --from=assets /app ./
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -tags builtinassets -mod=vendor -o frontend ./cmd/frontend/*.go

FROM --platform=${BUILDPLATFORM} gcr.io/distroless/static:latest
COPY --from=appbase /app/frontend /bin/frontend
ENTRYPOINT ["/bin/frontend"]
