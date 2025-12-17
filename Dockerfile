# syntax=docker/dockerfile:1

FROM golang:1.25-alpine AS builder
RUN apk --no-cache add ca-certificates
WORKDIR /app
COPY . .
RUN go build -o edgecdnx-api src/main.go

FROM scratch
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/edgecdnx-api /edgecdnx-api
EXPOSE 5555
ENTRYPOINT ["/edgecdnx-api"]