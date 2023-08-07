FROM golang:1.20.7 AS build

ARG CONTAINERD_VERSION=1.5.7
ARG NYDUS_VERSION=v2.1.5

ARG CONTAINERD_LINUX_AMD64_SHA256SUM="109fc95b86382065ea668005c376360ddcd8c4ec413e7abe220ae9f461e0e173"
ARG NYDUS_LINUX_AMD64_SHA256SUM="b7e4092d4db33f0bfe6cd8c67d59ccb393ab5d02db7140894297aa23fe9dad3d"

# Install acceld
COPY ./ /accel
RUN make -C /accel install-check-tools
RUN make -C /accel check
RUN make -C /accel

# Install containerd
RUN wget https://github.com/containerd/containerd/releases/download/v$CONTAINERD_VERSION/containerd-$CONTAINERD_VERSION-linux-amd64.tar.gz
RUN echo "$CONTAINERD_LINUX_AMD64_SHA256SUM  containerd-$CONTAINERD_VERSION-linux-amd64.tar.gz" | tee containerd-$CONTAINERD_VERSION-linux-amd64.tar.gz.sha256sum
RUN sha256sum -c containerd-$CONTAINERD_VERSION-linux-amd64.tar.gz.sha256sum
RUN mkdir -p /usr/local/bin/ && tar xzvf containerd-$CONTAINERD_VERSION-linux-amd64.tar.gz -C /usr/local/
RUN ls -l /usr/local/bin/

# Install nydus component
RUN wget https://github.com/dragonflyoss/image-service/releases/download/$NYDUS_VERSION/nydus-static-$NYDUS_VERSION-linux-amd64.tgz
RUN echo "$NYDUS_LINUX_AMD64_SHA256SUM  nydus-static-$NYDUS_VERSION-linux-amd64.tgz" | tee nydus-static-$NYDUS_VERSION-linux-amd64.tgz.sha256sum
RUN sha256sum -c nydus-static-$NYDUS_VERSION-linux-amd64.tgz.sha256sum
RUN tar xzvf nydus-static-$NYDUS_VERSION-linux-amd64.tgz && mv nydus-static/nydus-image /usr/local/bin/.

# Build acceld image
FROM photon:4.0
COPY --from=build /accel/acceld /accel/accelctl /usr/local/bin/containerd /usr/local/bin/nydus-image /usr/local/bin/
RUN mkdir /etc/containerd && /usr/local/bin/containerd config default > /etc/containerd/config.toml
COPY ./script/release/entrypoint.sh /entrypoint.sh

ENTRYPOINT [ "/entrypoint.sh" ]
