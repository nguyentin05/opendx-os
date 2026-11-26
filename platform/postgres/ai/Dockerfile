# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
FROM pgvector/pgvector:0.8.7-pg17@sha256:b513c48566e4777cd3370451400000c78eda6a08131d68f4c31d4a76f27bd8fd
ADD --checksum=sha256:ba8bd2cc4023bfab8a5f02859aaddf7670fad0d3fbb3c39dbb21f8dc275753ff \
    https://codeload.github.com/pgmq/pgmq/tar.gz/51468f6ba2afeb4a4a9a67e9a532f37b590a0929 /tmp/pgmq.tar.gz
RUN tar -xzf /tmp/pgmq.tar.gz --strip-components=2 -C /tmp \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/pgmq-extension/pgmq.control \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/pgmq-extension/sql/pgmq.sql \
    && install -m 644 /tmp/pgmq.control "$(pg_config --sharedir)/extension/pgmq.control" \
    && install -m 644 /tmp/sql/pgmq.sql "$(pg_config --sharedir)/extension/pgmq--1.13.2.sql" \
    && mkdir -p /usr/share/licenses/pgmq \
    && tar -xzf /tmp/pgmq.tar.gz --strip-components=1 -C /usr/share/licenses/pgmq \
        pgmq-51468f6ba2afeb4a4a9a67e9a532f37b590a0929/LICENSE \
    && rm -rf /tmp/pgmq.tar.gz /tmp/pgmq.control /tmp/sql
COPY --from=legal LICENSE NOTICE COPYRIGHT.txt THIRD_PARTY_LICENSES.txt /usr/share/licenses/opendx-os/
