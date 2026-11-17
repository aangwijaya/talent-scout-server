FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/server /server
# The server reads per-node models at start (MODELS_CONFIG_PATH, default
# ./config/models.yaml), so the file ships in the image next to the workdir.
COPY --from=build /src/config/models.yaml /app/config/models.yaml
WORKDIR /app
EXPOSE 8080
ENTRYPOINT ["/server"]
