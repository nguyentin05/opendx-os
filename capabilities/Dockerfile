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

# One image per command under cmd/: build with --build-arg SERVICE=<command>.
FROM golang:1.27.4-alpine AS builder
ARG SERVICE=cap-process
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/service ./cmd/${SERVICE}

FROM alpine:3.23
RUN adduser -D -u 10001 dxos
USER dxos
COPY --from=builder /out/service /usr/local/bin/service
# Apache-2.0 section 4: the license, notices and third-party license texts ship
# with the binaries. `legal` is the repository root, passed as a named build context.
COPY --from=legal LICENSE NOTICE COPYRIGHT.txt THIRD_PARTY_LICENSES.txt /usr/share/licenses/opendx-os/
ENTRYPOINT ["/usr/local/bin/service"]
