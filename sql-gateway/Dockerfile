ARG GO_IMAGE=golang:1.26.11-alpine
ARG MAVEN_IMAGE=maven:3.9-eclipse-temurin-17
ARG JAVA_IMAGE=eclipse-temurin:17-jre-jammy

FROM ${MAVEN_IMAGE} AS parser
WORKDIR /build
COPY sql-gateway/parser/pom.xml ./
COPY sql-gateway/parser/src ./src
RUN --mount=type=cache,target=/root/.m2 mvn -B -q package dependency:copy-dependencies -DincludeScope=runtime

FROM ${GO_IMAGE} AS build
WORKDIR /src
COPY object-storage/go.mod object-storage/go.sum ./
RUN --mount=type=cache,id=spark-control-center-go-mod,target=/go/pkg/mod,sharing=locked go mod download
COPY object-storage/cmd/sql-gateway ./cmd/sql-gateway
COPY object-storage/internal/sqlquery ./internal/sqlquery
COPY object-storage/internal/storage ./internal/storage
RUN --mount=type=cache,id=spark-control-center-go-mod,target=/go/pkg/mod,sharing=locked --mount=type=cache,id=spark-control-center-go-build,target=/root/.cache/go-build,sharing=locked CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/sql-gateway ./cmd/sql-gateway

FROM ${JAVA_IMAGE}
WORKDIR /app
ENV SPARK_LOCAL_IP=127.0.0.1
COPY --from=build /out/sql-gateway /app/sql-gateway
COPY --from=parser /build/target/sql-guard-1.6.1.jar /app/sql-guard.jar
COPY --from=parser /build/target/dependency/ /app/lib/
USER 65532:65532
EXPOSE 8083
ENTRYPOINT ["/app/sql-gateway"]
