# Stage 1: Build
FROM --platform=$BUILDPLATFORM golang:latest AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-w -s" -o api-prober .

# Stage 2: Final minimal image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/api-prober .

EXPOSE 8080

CMD ["./api-prober"]