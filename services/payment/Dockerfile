# Build context: services/ (shared Go module). Example:
#   minikube -p dbrcl image build -t dbrcl/payment:dev -f services/payment/Dockerfile services
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/payment ./payment

FROM scratch
COPY --from=build /out/payment /app/payment
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/app/payment"]
