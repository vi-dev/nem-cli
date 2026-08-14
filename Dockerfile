# Pinned by digest. Update with:
#   docker buildx imagetools inspect debian:bookworm-slim --format '{{.Manifest.Digest}}'
FROM debian:bookworm-slim@sha256:abd67ffcfa541b485a3dff59865ab629aa048a6c613e639d36e7456b0b229241

# The build toolchain `nem catalog build` shells out to. Removing any of these
# breaks source-built packages at runtime, not at image build time.
RUN apt-get update \
    && apt-get install --no-install-recommends -y \
        build-essential \
        perl \
        binutils \
        ca-certificates \
        curl \
        bzip2 \
        xz-utils \
    && rm -rf /var/lib/apt/lists/*

# goreleaser dockers_v2 places pre-built binaries in <TARGETPLATFORM>/nem
# within the build context (e.g. linux/amd64/nem, linux/arm64/nem).
ARG TARGETPLATFORM
COPY ${TARGETPLATFORM}/nem /usr/local/bin/nem

ENV NEM_HOME=/root/.nem
WORKDIR /work
ENTRYPOINT ["nem"]
CMD ["--help"]
