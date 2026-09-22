ARG GO_IMAGE=golang:1.24-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot

FROM ${GO_IMAGE} AS build
WORKDIR /src

COPY backend/go.mod backend/go.sum ./
RUN go mod download

COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/spark-control-center-backend ./cmd/server

FROM ${RUNTIME_IMAGE}
COPY --from=build /out/spark-control-center-backend /spark-control-center-backend
USER 65532:65532
EXPOSE 8081
ENTRYPOINT ["/spark-control-center-backend"]
