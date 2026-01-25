FROM golang:1.25.6-trixie AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/pdfproc ./cmd/server

FROM debian:trixie-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
    poppler-utils ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/pdfproc /app/pdfproc
ENV PORT=8080
EXPOSE 8080
CMD ["/app/pdfproc"]
