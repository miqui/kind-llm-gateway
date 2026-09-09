# syntax=docker/dockerfile:1
FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/llmgw ./cmd/llmgw

FROM scratch
COPY --from=build /out/llmgw /llmgw
EXPOSE 8080 8081
ENTRYPOINT ["/llmgw"]
