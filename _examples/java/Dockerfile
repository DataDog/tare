# Example: Java app with netty on distroless/java17.
#
# Netty bundles native .so files (epoll transport) inside its JARs.
# tare's scan unpacks JARs, finds these .so files, and verifies their
# shared library dependencies resolve.
#
# Build and check the good image (default target):
#   docker build -t example-java .
#   tare check -i example-java check.yaml
#
# Build and check the bad image:
#   docker build -t example-java-bad --target bad .
#   tare check -i example-java-bad check.yaml
#
# The bad image adds a JAR containing a JNI .so that links libcurl —
# not available in distroless/java17.

FROM maven:3.9-eclipse-temurin-17 AS build
WORKDIR /app
COPY pom.xml .
COPY src ./src
RUN mvn package -q -DskipTests

# Build a JNI .so that links libcurl, then package it in a JAR.
# This simulates a dependency that bundles a native library with
# unsatisfied system requirements.
FROM build AS build-bad
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev libcurl4-openssl-dev && rm -rf /var/lib/apt/lists/*
RUN printf '#include <curl/curl.h>\nvoid Java_example_Native_init(void) { curl_global_init(0); }\n' > /tmp/native.c && \
    gcc -shared -o /tmp/libexample_native.so /tmp/native.c -lcurl && \
    jar cf /tmp/native-jni-1.0.0.jar -C /tmp libexample_native.so

# Bad image: extra JAR with a .so that links libcurl — not in distroless/java.
FROM gcr.io/distroless/java17-debian12:nonroot AS bad
COPY --from=build /app/target/tare-java-example-1.0.0.jar /app/app.jar
COPY --from=build /app/target/lib /app/lib
COPY --from=build-bad /tmp/native-jni-1.0.0.jar /app/lib/native-jni-1.0.0.jar
ENTRYPOINT ["java", "-jar", "/app/app.jar"]

# Good image (default): netty's native .so links libc — present in distroless/java.
FROM gcr.io/distroless/java17-debian12:nonroot AS good
COPY --from=build /app/target/tare-java-example-1.0.0.jar /app/app.jar
COPY --from=build /app/target/lib /app/lib
ENTRYPOINT ["java", "-jar", "/app/app.jar"]
