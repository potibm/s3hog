# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/s3hog .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
COPY --from=build /out/s3hog /usr/local/bin/s3hog
EXPOSE 9000 8025
VOLUME ["/data"]
ENV S3_PORT=9000 \
    UI_PORT=8025 \
    S3_DATA_DIR=/data \
    AUTO_BUCKET=true \
    REGION=us-east-1
ENTRYPOINT ["/usr/local/bin/s3hog"]
