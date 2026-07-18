# Stage 1: Build
FROM --platform=$BUILDPLATFORM golang:latest AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-w -s" -o quadrasign .

# Stage 2: Final minimal image
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/quadrasign .

EXPOSE 8080

CMD ["./quadrasign"]