# Build context: services/ (shared Go module). Example:
#   minikube -p dbrcl image build -t dbrcl/router:dev -f services/router/Dockerfile services
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/router ./router

FROM scratch
COPY --from=build /out/router /app/router
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/router"]
