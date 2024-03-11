FROM golang:1.22.1 AS build

ARG NYDUS_VERSION=v2.2.4

ARG NYDUS_LINUX_AMD64_SHA256SUM="2fbebb016d6fbbc52fd575be1753d063ca9ada19ff2db02d405955a53a077b51"

ARG NYDUS_LINUX_ARM64_SHA256SUM="812398992eb8bb7993eb38a2bc6b222439037cfda9e7b6070937a7d55a2b189a"

# Install acceld
COPY ./ /accel
RUN make -C /accel build

# Install nydus component
RUN if [ "$(uname -m)" = "aarch64" ]; then \
    wget https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VERSION/nydus-static-$NYDUS_VERSION-linux-arm64.tgz \
    echo "$NYDUS_LINUX_ARM64_SHA256SUM  nydus-static-$NYDUS_VERSION-linux-arm64.tgz" | tee nydus-static-$NYDUS_VERSION-linux-arm64.tgz.sha256sum; \
    sha256sum -c nydus-static-$NYDUS_VERSION-linux-arm64.tgz.sha256sum; \
    tar xzvf nydus-static-$NYDUS_VERSION-linux-arm64.tgz && mv nydus-static/nydus-image /usr/local/bin; \
    else \
    wget https://github.com/dragonflyoss/nydus/releases/download/$NYDUS_VERSION/nydus-static-$NYDUS_VERSION-linux-amd64.tgz \
    echo "$NYDUS_LINUX_AMD64_SHA256SUM  nydus-static-$NYDUS_VERSION-linux-amd64.tgz" | tee nydus-static-$NYDUS_VERSION-linux-amd64.tgz.sha256sum; \
    sha256sum -c nydus-static-$NYDUS_VERSION-linux-amd64.tgz.sha256sum; \
    tar xzvf nydus-static-$NYDUS_VERSION-linux-amd64.tgz && mv nydus-static/nydus-image /usr/local/bin; \
    fi

# Build acceld image
FROM photon:5.0
COPY --from=build /accel/acceld /accel/accelctl /usr/local/bin/nydus-image /usr/local/bin/
COPY ./script/release/entrypoint.sh /entrypoint.sh

ENTRYPOINT [ "/entrypoint.sh" ]
