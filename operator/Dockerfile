# SPDX-License-Identifier: Apache-2.0
# Copyright 2026 DX-OS Open-Core Contributors
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# Manifest lifecycle CLI and Compose installer image: only the dxos-operator
# binary. The Kubernetes DXApplication controller image is Dockerfile.controller.
FROM golang:1.27.4-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/dxos-operator ./cmd/dxos-operator \
 && CGO_ENABLED=0 go build -trimpath -o /out/dxos-knowledge-dispatcher ./cmd/dxos-knowledge-dispatcher

FROM alpine:3.24
RUN adduser -D -u 10001 dxos && install -d -m 0700 -o dxos -g dxos /var/lib/dxos/package-artifacts
COPY --from=builder /out/ /usr/local/bin/
# Numeric user so Kubernetes can verify runAsNonRoot.
USER 10001:10001
ENTRYPOINT ["/usr/local/bin/dxos-operator"]
