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
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	container "cloud.google.com/go/container/apiv1"
	"cloud.google.com/go/container/apiv1/containerpb"
	cloudiam "cloud.google.com/go/iam"
	iam "cloud.google.com/go/iam/admin/apiv1"
	iampb "cloud.google.com/go/iam/apiv1/iampb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	"github.com/spf13/cobra"
	"golang.org/x/oauth2"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	_ "k8s.io/client-go/plugin/pkg/client/auth/exec"
	"k8s.io/client-go/rest"
	clientcmd "k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

var (
	projectID      string
	location       string
	clusterName    string
	kubeconfigpath string
)

// checkCmd represents the check command
var checkCmd = &cobra.Command{
	Use:   "check",
	Short: "Checks the Workload Identity configuration for various resources.",
	Long: `Performs a series of checks to validate the Workload Identity setup
for resources like Kubernetes Service Accounts (KSA) and workloads (Deployments, etc.).`,
	// This is a parent command, so it doesn't have a Run function.
}

func init() {
	rootCmd.AddCommand(checkCmd)

	checkCmd.PersistentFlags().StringVar(&projectID, "project", "", "GCP project ID (required)")
	checkCmd.PersistentFlags().StringVar(&location, "location", "", "GKE cluster location (region or zone) (required)")
	checkCmd.PersistentFlags().StringVar(&clusterName, "cluster", "", "GKE cluster name (required)")
	checkCmd.PersistentFlags().BoolFunc("local-kubeconfig", "Use local GKE cluster kubeconfig (optional)", getKubeconfig)

	checkCmd.MarkPersistentFlagRequired("project")
	checkCmd.MarkPersistentFlagRequired("location")
	checkCmd.MarkPersistentFlagRequired("cluster")
}

// getKubeconfig resolves the local kubeconfig path when --local-kubeconfig is enabled.
func getKubeconfig(val string) error {
	enabled, err := strconv.ParseBool(val)
	if err != nil {
		return fmt.Errorf("parsing --local-kubeconfig value %q: %w", val, err)
	}
	if !enabled {
		kubeconfigpath = ""
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("retrieving home directory: %w", err)
	}
	path := filepath.Join(homeDir, ".kube", "config")

	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("stat local kubeconfig: %w", err)
	}
	kubeconfigpath = path
	return nil
}

// newGKEClient creates a new GKE ClusterManagerClient with the necessary options,
// including the inspection token if it's set.
func newGKEClient(ctx context.Context) (*container.ClusterManagerClient, error) {
	gkeClient, err := container.NewClusterManagerClient(ctx, clientOptions()...)
	return gkeClient, err
}

// getGKECluster retrieves GKE cluster details.
func getGKECluster(ctx context.Context, client *container.ClusterManagerClient, project, location, cluster string) (*containerpb.Cluster, error) {
	req := &containerpb.GetClusterRequest{
		Name: fmt.Sprintf("projects/%s/locations/%s/clusters/%s", project, location, cluster),
	}
	return client.GetCluster(ctx, req)
}

var (
	errNilCluster       = errors.New("cluster is nil")
	errEndpointDisabled = errors.New("no accessible control plane endpoint enabled")
	errNoEndpointFound  = errors.New("no accessible control plane endpoint found")
)

type endpointKind int

const (
	endpointKindExternalIP endpointKind = iota
	endpointKindDNS
	endpointKindInternalIP
)

type clusterEndpoint struct {
	address string
	kind    endpointKind
}

// resolveEndpoint selects the preferred control plane endpoint for a GKE cluster:
// DNS endpoint (if external traffic is enabled) -> external IP endpoint -> internal IP endpoint.
func resolveEndpoint(cluster *containerpb.Cluster) (clusterEndpoint, error) {
	if cluster == nil {
		return clusterEndpoint{}, errNilCluster
	}

	// AllowExternalTraffic controls whether user traffic is enabled on the DNS endpoint;
	// Endpoint is always populated for Google-managed services even when false.
	if dnsCfg := cluster.GetControlPlaneEndpointsConfig().GetDnsEndpointConfig(); dnsCfg.GetAllowExternalTraffic() && dnsCfg.GetEndpoint() != "" {
		return clusterEndpoint{
			address: dnsCfg.GetEndpoint(),
			kind:    endpointKindDNS,
		}, nil
	}

	if ipCfg := cluster.GetControlPlaneEndpointsConfig().GetIpEndpointsConfig(); ipCfg != nil {
		if ipCfg.Enabled != nil && !ipCfg.GetEnabled() {
			return clusterEndpoint{}, fmt.Errorf("%w on cluster %q", errEndpointDisabled, cluster.GetName())
		}
		publicEnabled := ipCfg.EnablePublicEndpoint == nil || ipCfg.GetEnablePublicEndpoint()
		return resolveIPEndpoint(cluster, publicEnabled, ipCfg.GetPublicEndpoint(), ipCfg.GetPrivateEndpoint())
	}

	if privCfg := cluster.GetPrivateClusterConfig(); privCfg != nil {
		return resolveIPEndpoint(cluster, !privCfg.GetEnablePrivateEndpoint(), privCfg.GetPublicEndpoint(), privCfg.GetPrivateEndpoint())
	}

	if cluster.GetEndpoint() != "" {
		return clusterEndpoint{
			address: cluster.GetEndpoint(),
			kind:    endpointKindExternalIP,
		}, nil
	}

	return clusterEndpoint{}, fmt.Errorf("%w for cluster %q", errNoEndpointFound, cluster.GetName())
}

func resolveIPEndpoint(cluster *containerpb.Cluster, publicEnabled bool, publicEP, privateEP string) (clusterEndpoint, error) {
	if publicEnabled {
		if publicEP != "" {
			return clusterEndpoint{
				address: publicEP,
				kind:    endpointKindExternalIP,
			}, nil
		}
		if ep := cluster.GetEndpoint(); ep != "" && ep != privateEP {
			return clusterEndpoint{
				address: ep,
				kind:    endpointKindExternalIP,
			}, nil
		}
	}
	if privateEP != "" {
		return clusterEndpoint{
			address: privateEP,
			kind:    endpointKindInternalIP,
		}, nil
	}
	if cluster.GetEndpoint() != "" {
		return clusterEndpoint{
			address: cluster.GetEndpoint(),
			kind:    endpointKindInternalIP,
		}, nil
	}
	return clusterEndpoint{}, fmt.Errorf("%w for cluster %q", errNoEndpointFound, cluster.GetName())
}

func buildK8sRESTConfig(cluster *containerpb.Cluster, kubeconfigPath string, warnOut io.Writer) (*rest.Config, error) {
	if kubeconfigPath != "" {
		config, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
		if err != nil {
			return nil, fmt.Errorf("building config from kubeconfig: %w", err)
		}
		config.UserAgent = userAgent()
		return config, nil
	}

	if warnOut == nil {
		warnOut = os.Stderr
	}

	ep, err := resolveEndpoint(cluster)
	if err != nil {
		return nil, fmt.Errorf("resolving cluster endpoint: %w", err)
	}

	host := ep.address
	if !strings.HasPrefix(host, "https://") {
		host = "https://" + host
	}

	var tlsConfig rest.TLSClientConfig
	if ep.kind != endpointKindDNS {
		caDec, err := base64.StdEncoding.DecodeString(cluster.GetMasterAuth().GetClusterCaCertificate())
		if err != nil {
			return nil, fmt.Errorf("decoding cluster CA certificate: %w", err)
		}
		tlsConfig.CAData = caDec
	}

	if ep.kind == endpointKindInternalIP {
		fmt.Fprintf(warnOut, "⚠️  Warning: external endpoint is not enabled; falling back to internal endpoint %q. This may not work if accessing from a network not reachable to the cluster's private endpoint.\n", ep.address)
	}

	return &rest.Config{
		Host:            host,
		TLSClientConfig: tlsConfig,
		UserAgent:       userAgent(),
		ExecProvider: &clientcmdapi.ExecConfig{
			APIVersion:         "client.authentication.k8s.io/v1beta1",
			Command:            "gke-gcloud-auth-plugin",
			ProvideClusterInfo: true,
			InteractiveMode:    "Never",
		},
	}, nil
}

// getK8sClientset creates a Kubernetes clientset from GKE cluster data.
func getK8sClientset(cluster *containerpb.Cluster) (*kubernetes.Clientset, error) {
	config, err := buildK8sRESTConfig(cluster, kubeconfigpath, os.Stderr)
	if err != nil {
		return nil, err
	}
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return nil, fmt.Errorf("creating clientset from config: %w", err)
	}
	return clientset, nil
}

// gcpIAMPolicyReader fetches IAM policies for GCP projects and service accounts.
type gcpIAMPolicyReader interface {
	ProjectIAMPolicy(ctx context.Context, project string) (*iampb.Policy, error)
	GSAIAMPolicy(ctx context.Context, resource string) (*cloudiam.Policy, error)
}

type liveGCPIAMPolicyReader struct{}

func (liveGCPIAMPolicyReader) ProjectIAMPolicy(ctx context.Context, project string) (*iampb.Policy, error) {
	projectsClient, err := resourcemanager.NewProjectsClient(ctx, clientOptions()...)
	if err != nil {
		return nil, fmt.Errorf("creating Cloud Resource Manager client: %w", err)
	}
	defer projectsClient.Close()

	return projectsClient.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{
		Resource: "projects/" + project,
	})
}

func (liveGCPIAMPolicyReader) GSAIAMPolicy(ctx context.Context, resource string) (*cloudiam.Policy, error) {
	iamClient, err := iam.NewIamClient(ctx, clientOptions()...)
	if err != nil {
		return nil, fmt.Errorf("creating IAM client: %w", err)
	}
	defer iamClient.Close()

	return iamClient.GetIamPolicy(ctx, &iampb.GetIamPolicyRequest{
		Resource: resource,
	})
}

type ksaTarget struct {
	namespace string
	name      string
}

type ksaChecker struct {
	projectID string
	clientset kubernetes.Interface
	iam       gcpIAMPolicyReader
	out       io.Writer
}

// performKsaCheck carries out the actual validation for a given KSA.
func performKsaCheck(ctx context.Context, ksaNamespace, ksaName string, cluster *containerpb.Cluster, clientset kubernetes.Interface) error {
	checker := ksaChecker{
		projectID: projectID,
		clientset: clientset,
		iam:       liveGCPIAMPolicyReader{},
		out:       os.Stdout,
	}
	return checker.check(ctx, ksaTarget{namespace: ksaNamespace, name: ksaName}, cluster)
}

func (c *ksaChecker) check(ctx context.Context, target ksaTarget, cluster *containerpb.Cluster) error {
	out := c.out
	if out == nil {
		out = os.Stdout
	}

	fmt.Fprintf(out, "🔎 Starting GKE Workload Identity analysis for KSA: %s/%s\n", target.namespace, target.name)
	fmt.Fprintln(out, "-------------------------------------------------------------")

	// 1. Check GKE cluster for Workload Identity
	fmt.Fprintf(out, "1. Checking cluster '%s' in '%s'...\n", cluster.Name, cluster.Location)

	if cluster.WorkloadIdentityConfig == nil || cluster.WorkloadIdentityConfig.WorkloadPool == "" {
		return fmt.Errorf("Workload Identity is not enabled on cluster %q", cluster.Name)
	}
	workloadPool := cluster.WorkloadIdentityConfig.WorkloadPool
	fmt.Fprintf(out, "   ✅ Workload Identity is enabled. Workload Pool: %s\n", workloadPool)

	// 2. Check K8s Service Account and annotation
	fmt.Fprintf(out, "\n2. Checking K8s Service Account '%s/%s'...\n", target.namespace, target.name)

	ksa, err := c.clientset.CoreV1().ServiceAccounts(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("getting Kubernetes Service Account %q in namespace %q: %w", target.name, target.namespace, err)
	}
	fmt.Fprintf(out, "   ✅ Found KSA '%s/%s'.\n", target.namespace, target.name)

	gsaAnnotation := "iam.gke.io/gcp-service-account"
	gsaEmail, ok := ksa.Annotations[gsaAnnotation]

	legacySyntax := fmt.Sprintf("serviceAccount:%s[%s/%s]", workloadPool, target.namespace, target.name)
	principalSchema := fmt.Sprintf("%s/subject/ns/%s/sa/%s", workloadPool, target.namespace, target.name)

	// 3. Check IAM binding

	if !ok || gsaEmail == "" {
		fmt.Fprintf(out, "   ℹ️  KSA '%s/%s' is missing the '%s' annotation.\n", target.namespace, target.name, gsaAnnotation)
		fmt.Fprintln(out, "   ℹ️  This is not necessarily an error. Checking for direct IAM role bindings on the KSA principal...")

		fmt.Fprintln(out, "\n3. Checking for direct IAM bindings for KSA principal at the project level...")
		policy, err := c.iam.ProjectIAMPolicy(ctx, c.projectID)
		if err != nil {
			return fmt.Errorf("getting IAM policy for project %q: %w", c.projectID, err)
		}

		foundMember := ""
		for _, binding := range policy.Bindings {
			for _, m := range binding.Members {
				if strings.Contains(m, principalSchema) || strings.Contains(m, legacySyntax) {
					foundMember = m
					break
				}
			}
			if foundMember != "" {
				break
			}
		}

		if foundMember == "" {
			fmt.Fprintf(out, "   ❌ No direct IAM bindings found for KSA principal at the project level ('%s').\n", c.projectID)
			fmt.Fprintln(out, "   ℹ️  This is not necessarily an error if the principal is assigned role directly on the product.")
			fmt.Fprintln(out, "   ℹ️  If your workload needs permissions at the project level, you should either:")
			fmt.Fprintln(out, "	  1. Grant IAM roles directly to the KSA principal on the project level (recommended).\n		The principal syntax could be found at https://cloud.google.com/kubernetes-engine/docs/concepts/workload-identity#kubernetes-resources-iam-policies")
			fmt.Fprintf(out, "	  2. Annotate the KSA '%s/%s' to impersonate a GSA.\n", target.namespace, target.name)

		} else {
			fmt.Fprintf(out, "   ✅ Found direct IAM bindings for KSA principal '%s' at the project level.\n", foundMember)
			fmt.Fprintln(out, "\n🎉 Checks passed! The KSA has direct IAM role bindings at the project level.")
			fmt.Fprintln(out, "   Please ensure these roles provide the necessary permissions for your workload to function.")
		}
	} else {

		fmt.Fprintf(out, "   ✅ KSA is annotated with GSA: %s\n", gsaEmail)

		fmt.Fprintf(out, "\n3. Checking IAM binding for GSA '%s'...\n", gsaEmail)

		gsaResource, gsaProject := serviceAccountResource(gsaEmail)
		iamPolicy, err := c.iam.GSAIAMPolicy(ctx, gsaResource)

		if err != nil {
			return formatGSAIAMPolicyError(gsaIAMPolicyErrContext{
				gsaEmail:       gsaEmail,
				gsaProject:     gsaProject,
				clusterProject: c.projectID,
				legacySyntax:   legacySyntax,
			}, err)
		}

		bindingFound := iamPolicy.HasRole(legacySyntax, "roles/iam.workloadIdentityUser")

		if !bindingFound {
			return fmt.Errorf("IAM binding not found. Run the following command to fix:\n\ngcloud iam service-accounts add-iam-policy-binding %s \\\n  --role=roles/iam.workloadIdentityUser \\\n  --member=\"%s\"", gsaEmail, legacySyntax)
		}
		fmt.Fprintf(out, "   ✅ Found IAM binding for member '%s' with role roles/iam.workloadIdentityUser\n", legacySyntax)

		fmt.Fprintln(out, "-------------------------------------------------------------")
		fmt.Fprintln(out, "🎉 All checks passed! Your Workload Identity setup seems correct for this KSA.")
	}
	return nil
}

// serviceAccountResource builds the IAM resource name for a Google Service Account
// and returns the owning project ID if it can be inferred from the email address.
// For standard user-managed GSAs (<name>@<project-id>.iam.gserviceaccount.com), it extracts
// the owning project ID so cross-project GSAs work while preserving accurate 404 responses.
// Otherwise it falls back to the `-` project wildcard.
func serviceAccountResource(gsaEmail string) (string, string) {
	if parts := strings.Split(gsaEmail, "@"); len(parts) == 2 && parts[0] != "" {
		if gsaProject, ok := strings.CutSuffix(parts[1], ".iam.gserviceaccount.com"); ok && gsaProject != "" && !strings.ContainsAny(gsaProject, "./") {
			return fmt.Sprintf("projects/%s/serviceAccounts/%s", gsaProject, gsaEmail), gsaProject
		}
	}
	return fmt.Sprintf("projects/-/serviceAccounts/%s", gsaEmail), ""
}

type gsaIAMPolicyErrContext struct {
	gsaEmail       string
	gsaProject     string
	clusterProject string
	legacySyntax   string
}

func formatGSAIAMPolicyError(info gsaIAMPolicyErrContext, err error) error {
	if status.Code(err) == codes.PermissionDenied {
		projectNote := "the GSA's owning project"
		if info.gsaProject != "" {
			if info.clusterProject != "" && info.gsaProject != info.clusterProject {
				projectNote = fmt.Sprintf("cross-project owner %q (cluster project is %q)", info.gsaProject, info.clusterProject)
			} else {
				projectNote = fmt.Sprintf("project %q", info.gsaProject)
			}
		}
		return fmt.Errorf(
			"permission denied retrieving IAM policy for GSA %q: either your credentials lack iam.serviceAccounts.getIamPolicy on %s (common for cross-project GSAs or project-scoped tokens), or the service account/project does not exist.\nVerify manually with:\n\ngcloud iam service-accounts get-iam-policy %s\n\nand confirm 'roles/iam.workloadIdentityUser' is granted to %q: %w",
			info.gsaEmail, projectNote, info.gsaEmail, info.legacySyntax, err,
		)
	}
	return fmt.Errorf("failed to get IAM policy for GSA %q (does it exist?): %w", info.gsaEmail, err)
}

func tokenFromConfig() oauth2.TokenSource {
	if accessToken == "" {
		return nil
	}
	return oauth2.StaticTokenSource(&oauth2.Token{
		AccessToken: accessToken,
	})
}

type inspectionTokenCreds struct {
	token string
}

func (c inspectionTokenCreds) GetRequestMetadata(ctx context.Context, uri ...string) (map[string]string, error) {
	if c.token == "" {
		return nil, nil
	}
	return map[string]string{
		"x-goog-iam-authorization-token": c.token,
	}, nil
}

func (inspectionTokenCreds) RequireTransportSecurity() bool {
	return true
}

func clientOptions() []option.ClientOption {
	clientOpts := []option.ClientOption{
		option.WithUserAgent(userAgent()),
	}

	if inspectionToken != "" {
		clientOpts = append(clientOpts, option.WithGRPCDialOption(grpc.WithPerRPCCredentials(inspectionTokenCreds{
			token: inspectionToken,
		})))
	}

	if ts := tokenFromConfig(); ts != nil {
		clientOpts = append(clientOpts, option.WithTokenSource(ts))
	}

	return clientOpts
}
