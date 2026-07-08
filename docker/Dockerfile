# frontend (built once on the native builder arch; output is arch-independent)
FROM --platform=$BUILDPLATFORM node:22-alpine AS webbuild
WORKDIR /web
COPY src/web/package.json src/web/package-lock.json ./
RUN npm ci
COPY src/web/ .
RUN npm run build

# build (cross-compile from the native builder arch to the target arch)
FROM --platform=$BUILDPLATFORM golang:1.22-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /build
RUN apk add --no-cache git
COPY . .
COPY --from=webbuild /web/dist ./src/web/dist
RUN go mod tidy && CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/teslamate-dash ./src

# run (distroless, non-root, static)
FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/teslamate-dash /teslamate-dash
EXPOSE 4001
USER nonroot:nonroot
ENTRYPOINT ["/teslamate-dash"]
