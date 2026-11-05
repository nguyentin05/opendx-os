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

// Command dxos-operator plans and applies application manifest configuration.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opendx-os/opendx-os/operator/internal/manifest"
	"github.com/opendx-os/opendx-os/operator/internal/planner"
)

func main() {
	if len(os.Args) < 2 {
		printUsage()
		os.Exit(1)
	}

	command := os.Args[1]
	switch command {
	case "plan":
		runPlan(os.Args[2:])
	case "reconcile":
		runReconcile(os.Args[2:])
	case "version":
		fmt.Println("dxos-operator v0.1.0 (DX-OS Open-Core OLP 2026)")
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", command)
		printUsage()
		os.Exit(1)
	}
}

func printUsage() {
	fmt.Println("Usage: dxos-operator <command> [options]")
	fmt.Println("Commands:")
	fmt.Println("  plan <manifest.yaml>        Generate DDL and Hasura metadata plan without side effects")
	fmt.Println("  reconcile <apps-dir>        Plan and reconcile all application manifests in a directory")
	fmt.Println("  version                     Print operator version")
}

func runPlan(args []string) {
	fs := flag.NewFlagSet("plan", flag.ExitOnError)
	format := fs.String("format", "sql", "Output format: sql or hasura")
	fs.Parse(args)

	if fs.NArg() < 1 {
		fmt.Fprintln(os.Stderr, "Error: manifest file path required")
		os.Exit(1)
	}

	manifestPath := fs.Arg(0)
	app, err := manifest.ParseFile(manifestPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error parsing manifest: %v\n", err)
		os.Exit(1)
	}

	switch *format {
	case "sql":
		ddl, err := planner.GenerateDDL(app)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating DDL: %v\n", err)
			os.Exit(1)
		}
		fmt.Println(ddl)
	case "hasura":
		ops, err := planner.GenerateHasuraOperations(app)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error generating Hasura ops: %v\n", err)
			os.Exit(1)
		}
		b, _ := json.MarshalIndent(ops, "", "  ")
		fmt.Println(string(b))
	default:
		fmt.Fprintf(os.Stderr, "Unknown format: %s\n", *format)
		os.Exit(1)
	}
}

func runReconcile(args []string) {
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "Error: apps directory path required")
		os.Exit(1)
	}
	appsDir := args[0]
	matches, err := filepath.Glob(filepath.Join(appsDir, "*/manifest.yaml"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error scanning apps directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[dxos-operator] Reconciling %d applications in %s...\n", len(matches), appsDir)
	for _, m := range matches {
		app, err := manifest.ParseFile(m)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error parsing %s: %v\n", m, err)
			continue
		}
		fmt.Printf("[dxos-operator] -> Application %q (v%s) ready for reconciliation\n", app.Metadata.Name, app.Metadata.Version)
	}
}
