# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/retrieval ./cmd

FROM scratch
COPY --from=builder /out/retrieval /retrieval
EXPOSE 8080
ENTRYPOINT ["/retrieval"]
