# Example: static Go binary on distroless/static.
#
# Build and check the good image (default target):
#   docker build -t example-go-static .
#   tare check -i example-go-static check.yaml
#
# Build and check the bad image:
#   docker build -t example-go-static-bad --target bad .
#   tare check -i example-go-static-bad check.yaml
#
# The bad image builds with CGO enabled, producing a dynamically linked
# binary. distroless/static has no shared libraries, so tare's scan
# catches the missing libc.

FROM golang:1.24 AS build
WORKDIR /src
COPY main.go .
RUN go mod init example

# Static binary — no shared library dependencies.
FROM build AS build-good
RUN CGO_ENABLED=0 go build -o /app/server -ldflags='-s -w -X main.version=1.0.0' .

# Dynamic binary — links libc via CGO.
FROM build AS build-bad
RUN CGO_ENABLED=1 go build -o /app/server -ldflags='-X main.version=1.0.0' .

# Bad image: dynamic binary on distroless/static — missing libc.
FROM gcr.io/distroless/static:nonroot AS bad
COPY --from=build-bad /app/server /app/server
ENTRYPOINT ["/app/server"]

# Good image (default): static binary on distroless/static.
FROM gcr.io/distroless/static:nonroot AS good
COPY --from=build-good /app/server /app/server
ENTRYPOINT ["/app/server"]
