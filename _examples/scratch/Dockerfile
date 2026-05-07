# Example: Go binary on scratch with TLS.
#
# Build and check the good image (default target):
#   docker build -t example-scratch .
#   tare check -i example-scratch check.yaml
#
# Build and check the bad image:
#   docker build -t example-scratch-bad --target bad .
#   tare check -i example-scratch-bad check.yaml
#
# scratch is the most minimal base possible — no libc, no /etc, nothing.
# tare's harness injection means tests still work: tare injects bash,
# busybox, and BATS into the container at runtime.
#
# The good image copies CA certificates from the build stage so TLS works.
# The bad image omits them — tare catches the missing CA bundle via file
# existence, and the command test fails at runtime.

FROM golang:1.24 AS build
WORKDIR /src
COPY main.go .
RUN go mod init example
RUN CGO_ENABLED=0 go build -o /app/server -ldflags='-s -w' .

# Bad image: no CA certificates — TLS fails at runtime.
FROM scratch AS bad
COPY --from=build /app/server /app/server
ENTRYPOINT ["/app/server"]

# Good image (default): static binary + CA certs copied from build stage.
FROM scratch AS good
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /app/server /app/server
ENTRYPOINT ["/app/server"]
