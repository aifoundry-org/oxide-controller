FROM --platform=$BUILDPLATFORM golang:1.24.3-alpine3.21 AS build

ARG TARGETPLATFORM
ARG BUILDPLATFORM
ARG TARGETARCH
ARG TARGETOS

RUN apk update && \
    apk add --no-cache make git

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN make install TARGET=/oxide-controller OS=$(TARGETOS) ARCH=$(TARGETARCH)

FROM scratch
COPY --from=build /oxide-controller /oxide-controller

ENTRYPOINT ["/oxide-controller"]
