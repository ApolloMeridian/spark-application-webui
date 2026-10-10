ARG BUILD_IMAGE=golang:1.24-alpine
ARG RUNTIME_IMAGE=gcr.io/distroless/static-debian12:nonroot
FROM ${BUILD_IMAGE} AS build
WORKDIR /src
COPY object-storage/go.mod object-storage/go.sum ./
RUN go mod download
COPY object-storage/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /object-storage ./cmd/server

FROM ${RUNTIME_IMAGE}
COPY --from=build /object-storage /object-storage
USER 65532:65532
EXPOSE 8082
ENTRYPOINT ["/object-storage"]
