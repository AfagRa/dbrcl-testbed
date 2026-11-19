# Build context: services/ (shared Go module). Example:
#   minikube -p dbrcl image build -t dbrcl/checkout:dev -f services/checkout/Dockerfile services
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/checkout ./checkout

FROM scratch
COPY --from=build /out/checkout /app/checkout
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/checkout"]
