#!/usr/bin/env bash
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

# Prepends the project SPDX header to generated YAML files that lack one.
# controller-gen supports a header file for Go output only, so `make manifests`
# runs this over config/ to keep `make check-hygiene` green.
set -euo pipefail

header='# SPDX-License-Identifier: Apache-2.0
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
'

for dir in "$@"; do
    while IFS= read -r -d '' file; do
        if ! head -n 6 "$file" | grep -q 'SPDX-License-Identifier: Apache-2.0'; then
            tmp="$(mktemp)"
            printf '%s' "$header" > "$tmp"
            # Generated files begin with a document marker; keep it after the header.
            cat "$file" >> "$tmp"
            mv "$tmp" "$file"
            echo "[add-spdx-headers] added header: $file"
        fi
    done < <(find "$dir" -type f \( -name '*.yaml' -o -name '*.yml' \) -print0)
done
