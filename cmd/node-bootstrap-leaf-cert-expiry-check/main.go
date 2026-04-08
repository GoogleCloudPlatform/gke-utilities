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

// Command node-bootstrap-leaf-cert-expiry-check checks the expiry of node bootstrap leaf certificates
// stored in the metadata of GKE nodes.
package main

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/google/subcommands"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/container/v1"
	"sigs.k8s.io/yaml"
)

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")

	subcommands.Register(&CheckCommand{}, "")

	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}

type CheckCommand struct {
	project  string
	location string
	cluster  string
}

func (*CheckCommand) Name() string     { return "node-bootstrap-leaf-cert-expiry-check" }
func (*CheckCommand) Synopsis() string { return "Checks node leaf cert expiry" }
func (*CheckCommand) Usage() string {
	return `node-bootstrap-leaf-cert-expiry-check --project <project> --location <location> --cluster <cluster>`
}

func (c *CheckCommand) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.project, "project", "", "GCP Project ID")
	f.StringVar(&c.location, "location", "", "GKE Cluster Location (Zone or Region)")
	f.StringVar(&c.cluster, "cluster", "", "GKE Cluster Name")
}

func (c *CheckCommand) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.project == "" || c.location == "" || c.cluster == "" {
		log.Printf("Error: --project, --location, and --cluster must be specified.")
		return subcommands.ExitFailure
	}

	containerService, err := container.NewService(ctx)
	if err != nil {
		log.Printf("Error creating container service: %v", err)
		return subcommands.ExitFailure
	}

	computeService, err := compute.NewService(ctx)
	if err != nil {
		log.Printf("Error creating compute service: %v", err)
		return subcommands.ExitFailure
	}

	clusterName := fmt.Sprintf("projects/%s/locations/%s/clusters/%s", c.project, c.location, c.cluster)
	cluster, err := containerService.Projects.Locations.Clusters.Get(clusterName).Do()
	if err != nil {
		log.Printf("Error getting cluster: %v", err)
		return subcommands.ExitFailure
	}

	fmt.Printf("Checking cluster: %s\n", cluster.Name)

	for _, np := range cluster.NodePools {
		fmt.Printf("Node Pool: %s\n", np.Name)
		c.checkNodePoolTemplates(ctx, computeService, np)
	}

	return subcommands.ExitSuccess
}

func (c *CheckCommand) checkNodePoolTemplates(ctx context.Context, computeService *compute.Service, np *container.NodePool) {
	conditions := []string{
		fmt.Sprintf(`properties.labels.goog-k8s-cluster-name="%s"`, c.cluster),
		fmt.Sprintf(`properties.labels.goog-k8s-node-pool-name="%s"`, np.Name),
		fmt.Sprintf(`properties.labels.goog-k8s-cluster-location="%s"`, c.location),
	}
	filter := strings.Join(conditions, " AND ")

	found := false
	resp := computeService.InstanceTemplates.AggregatedList(c.project).Filter(filter)
	err := resp.Pages(ctx, func(page *compute.InstanceTemplateAggregatedList) error {
		for _, scopedList := range page.Items {
			if len(scopedList.InstanceTemplates) > 0 {
				found = true
				for _, template := range scopedList.InstanceTemplates {
					if template.Properties != nil && template.Properties.Metadata != nil {
						c.checkTemplateMetadata(template.Properties.Metadata.Items, fmt.Sprintf("Template: %s", template.Name))
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("Error listing instance templates with filter: %v", err)
		return
	}

	if !found {
		fmt.Printf("  No templates found for node pool %s\n", np.Name)
	}
}

func (c *CheckCommand) checkTemplateMetadata(items []*compute.MetadataItems, source string) {
	for _, item := range items {
		if item.Key == "kube-env" && item.Value != nil {
			kubeEnv := *item.Value
			c.extractAndCheckCerts(kubeEnv, source)
		}
	}
}

func (c *CheckCommand) extractAndCheckCerts(kubeEnv, source string) {
	var env map[string]string
	if err := yaml.Unmarshal([]byte(kubeEnv), &env); err != nil {
		log.Printf("Error unmarshaling kube-env as YAML from %s: %v", source, err)
		return
	}

	for _, key := range []string{"TPM_BOOTSTRAP_CERT", "KUBELET_CERT"} {
		if val, ok := env[key]; ok {
			certData, err := base64.StdEncoding.DecodeString(val)
			if err != nil {
				log.Printf("Error decoding cert for %s in %s: %v", key, source, err)
				continue
			}
			c.checkCertExpiration(certData, key, source)
		}
	}
}

func (c *CheckCommand) checkCertExpiration(data []byte, key, source string) {
	block, _ := pem.Decode(data)
	var certBytes []byte
	if block != nil {
		certBytes = block.Bytes
	} else {
		certBytes = data
	}

	cert, err := x509.ParseCertificate(certBytes)
	if err != nil {
		log.Printf("Error parsing certificate for %s in %s: %v", key, source, err)
		return
	}

	fmt.Printf("  [%s] %s: expires on %s (Valid for %v)\n", source, key, cert.NotAfter.Format(time.RFC3339), time.Until(cert.NotAfter))
}
