// Copyright 2026 Google LLC
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

package cmd

import (
	"fmt"
	"os"
	"regexp"
	"runtime/debug"
	"strings"

	"github.com/spf13/cobra"
)

const (
	userAgentProduct = "gke-wif-troubleshooter"
	defaultVersion   = "v0.1.0"
)

var (
	accessToken     string
	inspectionToken string
	// version can be overridden at build time via:
	// -ldflags "-X github.com/GoogleCloudPlatform/gke-utilities/gke-wif-troubleshooter/cmd.version=v0.1.0"
	version = defaultVersion

	pseudoVersionPattern = regexp.MustCompile(`(?:^|[.-])\d{14}-[0-9a-f]{12}$`)
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "gke-wif-troubleshooter",
	Short: "A CLI tool to troubleshoot Workload Identity Federation for GKE.",
	Long: `gke-wif-troubleshooter is a command-line tool designed to diagnose and
resolve common configuration issues with Workload Identity Federation on Google
Kubernetes Engine (GKE).

It helps you verify that your GKE clusters, Kubernetes Service Accounts, and
Google Service Accounts are correctly configured to allow your GKE workloads to
securely access Google Cloud services.`,
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func isPseudoVersion(v string) bool {
	v, _, _ = strings.Cut(strings.TrimSpace(v), "+")
	return pseudoVersionPattern.MatchString(v)
}

func resolveVersion(override, buildInfoVersion string) string {
	candidate := override
	if candidate == "" || candidate == defaultVersion {
		if buildInfoVersion != "" && buildInfoVersion != "(devel)" && !isPseudoVersion(buildInfoVersion) {
			candidate = buildInfoVersion
		}
	}
	return sanitizeVersion(candidate)
}

func sanitizeVersion(v string) string {
	v = strings.TrimSpace(v)
	v, _, _ = strings.Cut(v, "+")
	if v == "" || v == "(devel)" {
		return defaultVersion
	}
	for _, r := range v {
		isAlphaNum := (r >= '0' && r <= '9') || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z')
		if !isAlphaNum && r != '.' && r != '_' && r != '-' {
			return defaultVersion
		}
	}
	return v
}

func appVersion() string {
	var buildInfoVersion string
	if info, ok := debug.ReadBuildInfo(); ok {
		buildInfoVersion = info.Main.Version
	}
	return resolveVersion(version, buildInfoVersion)
}

func userAgent() string {
	return fmt.Sprintf("%s/%s", userAgentProduct, appVersion())
}

func init() {
	rootCmd.Version = appVersion()
	accessToken = os.Getenv("ACCESS_TOKEN")
	inspectionToken = os.Getenv("INSPECTION_TOKEN")
}
