# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o edgecdnx-api cmd/main.go

FROM scratch
COPY --from=builder /app/edgecdnx-api /edgecdnx-api
EXPOSE 5555
ENTRYPOINT ["/edgecdnx-api"]