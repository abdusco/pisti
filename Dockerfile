FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /pisti .

FROM gcr.io/distroless/static:nonroot
LABEL org.opencontainers.image.source="https://github.com/abdusco/pisti"
LABEL org.opencontainers.image.description="Two-player Pişti with private tables, Ada bot play, and an installable bilingual client."
COPY --from=build /pisti /pisti
ENV PORT=8080
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/pisti"]
