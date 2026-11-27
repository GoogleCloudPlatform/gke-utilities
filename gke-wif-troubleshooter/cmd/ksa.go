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
	"context"
	"log"

	"github.com/spf13/cobra"
)

var ksaNamespace string

// ksaCmd represents the ksa command
var ksaCmd = &cobra.Command{
	Use:   "ksa <ksa-name>",
	Short: "Checks the Workload Identity configuration for a specific Kubernetes Service Account (KSA).",
	Long: `Analyzes a given Kubernetes Service Account (KSA) to verify its Workload Identity setup.

It checks for the required annotation on the KSA and the corresponding IAM binding on the associated Google Service Account (GSA).`,

	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		ksaName := args[0]
		ctx := context.Background()

		gkeClient, err := newGKEClient(ctx)
		if err != nil {
			log.Fatalf("❌ Failed to create GKE client: %v", err)
		}
		defer gkeClient.Close()

		cluster, err := getGKECluster(ctx, gkeClient, projectID, location, clusterName)
		if err != nil {
			log.Fatalf("❌ Failed to get GKE cluster details: %v", err)
		}

		clientset, err := getK8sClientset(cluster)
		if err != nil {
			log.Fatalf("❌ Failed to create Kubernetes clientset: %v", err)
		}

		if err := performKsaCheck(ctx, ksaNamespace, ksaName, cluster, clientset); err != nil {
			log.Fatalf("❌ Check failed: %v", err)
		}
	},
}

func init() {
	checkCmd.AddCommand(ksaCmd)
	ksaCmd.Flags().StringVarP(&ksaNamespace, "namespace", "n", "default", "Kubernetes namespace of the service account")
}
