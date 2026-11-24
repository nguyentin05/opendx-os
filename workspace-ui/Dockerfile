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

FROM node:22-alpine AS builder
WORKDIR /app
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

# The unprivileged variant runs as the nginx user and listens on 8080.
FROM nginxinc/nginx-unprivileged:1.31-alpine@sha256:b5047b9b76845eac124358373c6852377c52e4b05834a47647e955dd91cb8f1e
# The base image lags Alpine security fixes; images.yml fails on any left over.
USER root
RUN apk upgrade --no-cache
# The entrypoint renders templates into conf.d at start. DXOS_OIDC_ORIGIN is the
# Keycloak origin the browser may call; set it when Keycloak is not on localhost:8085.
ENV DXOS_OIDC_ORIGIN=http://localhost:8085
COPY nginx.conf /etc/nginx/templates/default.conf.template
COPY --from=builder /app/dist /usr/share/nginx/html
# Apache-2.0 section 4: the license, notices and third-party license texts ship
# with the binaries. `legal` is the repository root, passed as a named build context.
COPY --from=legal LICENSE NOTICE COPYRIGHT.txt THIRD_PARTY_LICENSES.txt /usr/share/licenses/opendx-os/
USER nginx
EXPOSE 8080
CMD ["nginx", "-g", "daemon off;"]
