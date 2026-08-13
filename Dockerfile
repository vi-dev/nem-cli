FROM golang:1.26-bookworm AS build
ENV GOTOOLCHAIN=auto
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/nem ./cmd/nem

FROM debian:bookworm-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      build-essential perl binutils ca-certificates curl bzip2 xz-utils \
 && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/nem /usr/local/bin/nem
ENV NEM_HOME=/root/.nem
WORKDIR /work
ENTRYPOINT ["nem"]
CMD ["--help"]
