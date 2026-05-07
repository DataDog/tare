# Example: Python app with native C extensions on distroless/python3.
#
# Build and check the good image (default target):
#   docker build -t example-python .
#   tare check -i example-python check.yaml
#
# Build and check the bad image:
#   docker build -t example-python-bad --target bad .
#   tare check -i example-python-bad check.yaml
#
# The good image installs lxml from a manylinux wheel — the wheel bundles
# its own copies of libxml2/libxslt, so the .so files are self-contained.
#
# The bad image builds lxml from source. The resulting .so files link the
# system's libxml2.so and libxslt.so, which aren't in distroless/python3.
# This is a realistic failure: someone builds from source (for a patch,
# optimization, or because no wheel exists for their platform) and forgets
# that the native extensions now depend on system libraries.

FROM python:3.11-slim AS build-base
WORKDIR /app
COPY app.py requirements.txt ./

# Wheels: manylinux .so files are self-contained.
FROM build-base AS build-good
RUN pip install --target=/app/deps -r requirements.txt

# Source build: .so files link system libxml2/libxslt.
FROM build-base AS build-bad
RUN apt-get update && apt-get install -y --no-install-recommends \
    libc6-dev zlib1g-dev libxml2-dev libxslt1-dev gcc && rm -rf /var/lib/apt/lists/*
RUN pip install --target=/app/deps --no-binary lxml -r requirements.txt

# Bad image: source-built native extensions, missing libxml2/libxslt.
FROM gcr.io/distroless/python3-debian12:nonroot AS bad
COPY --from=build-bad /app /app
ENV PYTHONPATH=/app/deps
ENTRYPOINT ["python3", "/app/app.py"]

# Good image (default): wheel-installed native extensions, deps self-contained.
FROM gcr.io/distroless/python3-debian12:nonroot AS good
COPY --from=build-good /app /app
ENV PYTHONPATH=/app/deps
ENTRYPOINT ["python3", "/app/app.py"]
