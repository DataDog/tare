# Example: image whose checks need runtime context that the image alone
# doesn't provide.
#
# The app exits non-zero without:
#   - an APP_ENV variable (provided by deployment, not the image)
#   - a config file at /etc/myapp/config.yaml (mounted from a k8s
#     configmap or equivalent — never baked into the image)
#
# Without tare.runtime, `tare check` would fail the command test. The
# check.yaml here uses tare.runtime.env and tare.runtime.binds to
# emulate the deployment environment, mirroring how this image actually
# runs in production.
#
# Build and check:
#   docker build -t example-runtime .
#   tare check -i example-runtime check.yaml

FROM golang:1.23 AS build
WORKDIR /src
COPY main.go .
RUN CGO_ENABLED=0 go build -o /server main.go

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /server /app/server
ENTRYPOINT ["/app/server"]
