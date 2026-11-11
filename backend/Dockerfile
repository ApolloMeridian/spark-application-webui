ARG GO_IMAGE=golang:1.26.11-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN --mount=type=cache,id=spark-control-center-go-mod,target=/go/pkg/mod,sharing=locked go mod download

COPY backend/ ./
RUN --mount=type=cache,id=spark-control-center-go-mod,target=/go/pkg/mod,sharing=locked --mount=type=cache,id=spark-control-center-go-build,target=/root/.cache/go-build,sharing=locked CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/spark-control-center-backend ./cmd/server

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/spark-control-center-backend /spark-control-center-backend
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/spark-control-center-backend"]
