# Example: dynamically linked C binary on distroless/cc.
#
# Build and check the good image (default target):
#   docker build -t example-c-dynamic .
#   tare check -i example-c-dynamic check.yaml
#
# Build and check the bad image:
#   docker build -t example-c-dynamic-bad --target bad .
#   tare check -i example-c-dynamic-bad check.yaml
#
# distroless/cc includes libc, libgcc, libstdc++, and libssl, so a
# standard C binary links fine. The bad image adds a libcurl dependency —
# libcurl is not in distroless/cc, so tare's scan catches it.

FROM debian:bookworm-slim AS build
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev libcurl4-openssl-dev && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY main.c tls.c ./
RUN mkdir -p /app && gcc -o /app/server main.c
RUN gcc -o /app/curl-server tls.c -lcurl

# Bad image: binary links libcurl, not available in distroless/cc.
FROM gcr.io/distroless/cc-debian12:nonroot AS bad
COPY --from=build /app/curl-server /app/server
ENTRYPOINT ["/app/server"]

# Good image (default): standard C binary, deps satisfied by distroless/cc.
FROM gcr.io/distroless/cc-debian12:nonroot AS good
COPY --from=build /app/server /app/server
ENTRYPOINT ["/app/server"]
