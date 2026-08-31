# syntax=docker/dockerfile:1
#
# seedwright — first-run-friendly image.
#
# The app boots with NO config file (first-run defaults: ephemeral memory
# storage + http://127.0.0.1:1234) and the onboarding extension is on by
# default, so `docker run` gives you a working UI and a setup wizard at
# /onboarding. The wizard writes config.yaml into /data — mount a volume
# there to survive restarts:
#
#   docker run -d --name seedwright \
#     -p 8080:8080 \
#     -v seedwright-data:/data \
#     seedwright
#
# sdcpp itself runs outside the container (GPU); point the wizard at it.

FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 go build -ldflags="-s -w" -o /out/seedwright ./cmd/app

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates \
      graphicsmagick \
    && rm -rf /var/lib/apt/lists/*
# graphicsmagick is optional: the imageproc extension falls back to
# passthrough (with a warning) when gm is missing. It is needed for
# crop-printing via the printer extension.
# cups-client (lp/lpstat) is deliberately NOT installed: CUPS printing
# targets a CUPS server on the host or network — install it and forward
# /etc/cups only if you actually print from the container.

COPY --from=build /out/seedwright /usr/local/bin/seedwright

RUN useradd --create-home --shell /usr/sbin/nologin seedwright \
    && mkdir -p /data && chown seedwright:seedwright /data
USER seedwright
WORKDIR /data

# config.yaml is read from (and written to) /data/config.yaml.
VOLUME /data
EXPOSE 8080
ENTRYPOINT ["seedwright"]
