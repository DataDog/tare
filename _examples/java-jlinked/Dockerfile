# Example: Java app with a jlinked custom JRE on distroless/base.
#
# The plain `_examples/java` ships on `gcr.io/distroless/java17-debian12`, which
# bundles a full JRE — ~235 MB of which is runtime, only ~2 MB is the app.
# This example shows the contrast: use jdeps + jlink to build a minimal
# custom JRE containing only the modules the app actually uses, then ship
# it on `gcr.io/distroless/base-debian12` (no JRE, just the libc/openssl
# the JVM needs).
#
# Expect a tare weight of roughly 60–90 MB instead of 235 MB.
#
# Build and check:
#   docker build -t example-java-jlinked .
#   tare check -i example-java-jlinked check.yaml

FROM maven:3.9-eclipse-temurin-17 AS build
WORKDIR /app
COPY pom.xml .
COPY src ./src
RUN mvn package -q -DskipTests

# Build a custom JRE containing only the modules the app needs.
# jdk.unsupported is added because netty uses sun.misc.Unsafe.
FROM eclipse-temurin:17-jdk AS jlink
COPY --from=build /app/target/tare-java-example-1.0.0.jar /scratch/app.jar
COPY --from=build /app/target/lib /scratch/lib
RUN MODULES=$(jdeps --print-module-deps --ignore-missing-deps --recursive \
        --multi-release 17 --class-path '/scratch/lib/*' /scratch/app.jar) && \
    jlink --add-modules "$MODULES,jdk.unsupported" \
        --strip-debug --no-man-pages --no-header-files --compress=2 \
        --output /jre

FROM gcr.io/distroless/base-debian12:nonroot
COPY --from=jlink /jre /jre
COPY --from=build /app/target/tare-java-example-1.0.0.jar /app/app.jar
COPY --from=build /app/target/lib /app/lib
ENV PATH=/jre/bin:$PATH
ENTRYPOINT ["/jre/bin/java", "-jar", "/app/app.jar"]
