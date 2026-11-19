# Build context: services/ (shared Go module). Example:
#   minikube -p dbrcl image build -t dbrcl/egress-sink:dev -f services/egress-sink/Dockerfile services
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/egress-sink ./egress-sink

FROM scratch
COPY --from=build /out/egress-sink /app/egress-sink
USER 65532:65532
EXPOSE 9000 9100
ENTRYPOINT ["/app/egress-sink"]
