FROM golang:1.25-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/duraclaw ./cmd/duraclaw

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/duraclaw /duraclaw
USER nonroot:nonroot
EXPOSE 8080
ENTRYPOINT ["/duraclaw"]
