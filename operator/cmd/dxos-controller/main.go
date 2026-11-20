// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 DX-OS Open-Core Contributors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Command dxos-controller is the Kubernetes operator for DXApplication
// resources. It runs the controller-runtime manager only; manifest CLI and
// Compose tooling live in the dxos-operator binary.
package main

import (
	"os"

	"github.com/opendx-os/opendx-os/operator/internal/manager"
)

func main() {
	os.Exit(manager.Run(os.Args[1:], os.Stdout, os.Stderr))
}
