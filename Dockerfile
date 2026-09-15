# syntax=docker/dockerfile:1

FROM golang:1.27.1-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build

WORKDIR /src
COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /tripit-exporter ./cmd/tripit-exporter


FROM gcr.io/distroless/static:nonroot@sha256:e2e927ec666bae08560abb3c55d0659eceabb657f56b6782ab500a9fc7f555e3

COPY --from=build /tripit-exporter /tripit-exporter

ENTRYPOINT ["/tripit-exporter"]
