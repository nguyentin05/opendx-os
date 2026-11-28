# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# PostgreSQL 17 on Alpine, the same base the default stack and Kubernetes run,
# with the two extensions the Python AI runtime needs: pgvector and PGMQ. The
# image serves every installation, with or without AI.
FROM postgres:17-alpine@sha256:f9cf3e1f0066f0eb0c48707d864f0ac5941e9ff454a072077d97a4d278d07d73 AS extensions
# Build tools exist only in this stage and are removed before the runtime image.
RUN apk add --no-cache --virtual .build-deps \
      build-base clang llvm \
    && apk upgrade --no-cache
# pgvector 0.8.7 from its release tag. LLVM is present so PGXS can emit
# bitcode; extension bitcode is optional and the build does not need it.
ADD --checksum=sha256:cac0b10c360f05b2d521200105ba3697e773d4cd3731f5a915a7e37ebe0bea85 \
    https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.7.tar.gz /tmp/pgvector.tar.gz
ADD --checksum=sha256:ba8bd2cc4023bfab8a5f02859aaddf7670fad0d3fbb3c39dbb21f8dc275753ff \
    https://codeload.github.com/pgmq/pgmq/tar.gz/51468f6ba2afeb4a4a9a67e9a532f37b590a0929 /tmp/pgmq.tar.gz
RUN cd /tmp \
    && tar -xzf /tmp/pgvector.tar.gz \
    && make -C /tmp/pgvector-0.8.7 with_llvm=no \
    && make -C /tmp/pgvector-0.8.7 install with_llvm=no \
    && tar -xzf /tmp/pgmq.tar.gz --strip-components=2 -C /tmp \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/pgmq-extension/pgmq.control \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/pgmq-extension/sql/pgmq.sql \
    && install -m 644 /tmp/pgmq.control "$(pg_config --sharedir)/extension/pgmq.control" \
    && install -m 644 /tmp/sql/pgmq.sql "$(pg_config --sharedir)/extension/pgmq--1.13.2.sql" \
    && mkdir -p /usr/share/licenses/pgmq \
    && tar -xzf /tmp/pgmq.tar.gz --strip-components=1 -C /usr/share/licenses/pgmq \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/LICENSE \
    && rm -rf /tmp/pgvector.tar.gz /tmp/pgvector-0.8.7 /tmp/pgmq.tar.gz /tmp/pgmq.control /tmp/sql \
    && rm -f /usr/local/bin/gosu /usr/local/bin/gosu.asc \
    && apk del .build-deps \
    && rm -rf /var/cache/apk/*
COPY --from=legal LICENSE NOTICE COPYRIGHT.txt THIRD_PARTY_LICENSES.txt /usr/share/licenses/opendx-os/

FROM extensions AS runtime
# Apply Alpine security updates in the stage rebuilt by image CI.
RUN apk upgrade --no-cache
# The upstream entrypoint only uses gosu to drop privileges when started as
# root. Run as postgres directly so no root-run Go helper ships in the image.
USER postgres
