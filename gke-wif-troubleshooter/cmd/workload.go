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
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"cloud.google.com/go/container/apiv1/containerpb"
	"github.com/spf13/cobra"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

const gkeMetadataServerLabel = "iam.gke.io/gke-metadata-server-enabled"

var (
	workloadNamespace          string
	workloadType               string
	errUnsupportedWorkloadType = errors.New("unsupported workload type")
	errMetadataServerDisabled  = errors.New("node does not have GKE Metadata Server enabled")
)

// workloadCmd represents the workload command
var workloadCmd = &cobra.Command{
	Use:   "workload <workload-name>",
	Short: "Checks the Workload Identity configuration for a given Kubernetes workload.",
	Long: `Analyzes a Kubernetes workload (e.g., Pod, Deployment, StatefulSet, CronJob) to verify its Workload Identity setup.

	It performs the following checks:
		- Identifies the Kubernetes Service Account (KSA) used by the workload and then performs all the necessary checks on that KSA.
		- Verifies that the workload's pod(s) are running on node(s) with GKE Metadata Server enabled (skipped for Autopilot clusters).
		- Checks for known configuration issues
		`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true
		workloadName := args[0]
		ctx := cmd.Context()

		gkeClient, err := newGKEClient(ctx)
		if err != nil {
			return fmt.Errorf("creating GKE client: %w", err)
		}
		defer gkeClient.Close()

		cluster, err := getGKECluster(ctx, gkeClient, projectID, location, clusterName)
		if err != nil {
			return fmt.Errorf("getting GKE cluster details: %w", err)
		}

		clientset, err := getK8sClientset(cluster)
		if err != nil {
			return fmt.Errorf("creating Kubernetes clientset: %w", err)
		}

		workload, err := fetchWorkload(ctx, clientset, workloadTarget{
			namespace: workloadNamespace,
			name:      workloadName,
			kind:      workloadType,
		})
		if err != nil {
			return fmt.Errorf("getting KSA from workload: %w", err)
		}

		fmt.Printf("ℹ️ Workload %q in namespace %q is using Kubernetes Service Account %q.\n\n", workload.name, workload.namespace, workload.serviceAccountName)

		var errs []error
		if err := performKsaCheck(ctx, workload.namespace, workload.serviceAccountName, cluster, clientset); err != nil {
			errs = append(errs, fmt.Errorf("checking KSA %q: %w", workload.serviceAccountName, err))
		}

		if cluster.GetWorkloadIdentityConfig().GetWorkloadPool() != "" {
			checker := &workloadNodeChecker{
				clientset: clientset,
				out:       os.Stdout,
			}
			if err := checker.checkMetadataServer(ctx, cluster, workload); err != nil {
				errs = append(errs, fmt.Errorf("checking node GKE Metadata Server: %w", err))
			}
		}

		return errors.Join(errs...)
	},
}

type workloadTarget struct {
	namespace string
	name      string
	kind      string
}

type workloadDetails struct {
	kind               string
	namespace          string
	name               string
	uid                types.UID
	selector           *metav1.LabelSelector
	serviceAccountName string
	pod                *corev1.Pod
}

func fetchWorkload(ctx context.Context, clientset kubernetes.Interface, target workloadTarget) (workloadDetails, error) {
	details := workloadDetails{
		namespace: target.namespace,
		name:      target.name,
	}
	var err error

	switch strings.ToLower(target.kind) {
	case "deployment", "deploy":
		var w *appsv1.Deployment
		w, err = clientset.AppsV1().Deployments(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "Deployment"
			details.uid = w.UID
			details.selector = w.Spec.Selector
			details.serviceAccountName = w.Spec.Template.Spec.ServiceAccountName
		}
	case "statefulset", "sts":
		var w *appsv1.StatefulSet
		w, err = clientset.AppsV1().StatefulSets(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "StatefulSet"
			details.uid = w.UID
			details.selector = w.Spec.Selector
			details.serviceAccountName = w.Spec.Template.Spec.ServiceAccountName
		}
	case "daemonset", "ds":
		var w *appsv1.DaemonSet
		w, err = clientset.AppsV1().DaemonSets(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "DaemonSet"
			details.uid = w.UID
			details.selector = w.Spec.Selector
			details.serviceAccountName = w.Spec.Template.Spec.ServiceAccountName
		}
	case "job":
		var w *batchv1.Job
		w, err = clientset.BatchV1().Jobs(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "Job"
			details.uid = w.UID
			details.selector = w.Spec.Selector
			details.serviceAccountName = w.Spec.Template.Spec.ServiceAccountName
		}
	case "cronjob", "cj":
		var w *batchv1.CronJob
		w, err = clientset.BatchV1().CronJobs(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "CronJob"
			details.uid = w.UID
			details.serviceAccountName = w.Spec.JobTemplate.Spec.Template.Spec.ServiceAccountName
		}
	case "pod", "po":
		var p *corev1.Pod
		p, err = clientset.CoreV1().Pods(target.namespace).Get(ctx, target.name, metav1.GetOptions{})
		if err == nil {
			details.kind = "Pod"
			details.uid = p.UID
			details.serviceAccountName = p.Spec.ServiceAccountName
			details.pod = p
		}
	default:
		return workloadDetails{}, fmt.Errorf("%w %q", errUnsupportedWorkloadType, target.kind)
	}

	if err != nil {
		return workloadDetails{}, fmt.Errorf("getting workload %q (%s) in namespace %q: %w", target.name, target.kind, target.namespace, err)
	}

	// If the service account is not specified in the pod spec, it defaults to "default".
	if details.serviceAccountName == "" {
		details.serviceAccountName = "default"
	}

	return details, nil
}

type workloadNodeChecker struct {
	clientset kubernetes.Interface
	out       io.Writer
}

type nodeCheckResult struct {
	enabled     bool
	notFoundErr error
}

type ownerTarget struct {
	kind string
	name string
	uid  types.UID
}

func (c *workloadNodeChecker) checkMetadataServer(ctx context.Context, cluster *containerpb.Cluster, workload workloadDetails) error {
	out := c.out
	if out == nil {
		out = os.Stdout
	}

	fmt.Fprintf(out, "\n4. Checking GKE Metadata Server on node(s) for %s %q in namespace %q...\n", workload.kind, workload.name, workload.namespace)

	if cluster.GetAutopilot().GetEnabled() {
		fmt.Fprintf(out, "   ℹ️  Skipping node GKE Metadata Server check: cluster %q is an Autopilot cluster (GKE Metadata Server is enabled by default).\n", cluster.GetName())
		return nil
	}

	pods, err := c.pods(ctx, workload)
	if err != nil {
		return err
	}
	if len(pods) == 0 {
		fmt.Fprintf(out, "   ℹ️  No pods found for %s %q in namespace %q; skipping node GKE Metadata Server check.\n", workload.kind, workload.name, workload.namespace)
		return nil
	}

	nodeCache := make(map[string]nodeCheckResult)
	var missingPods []string
	checkedPods := 0

	for _, pod := range pods {
		nodeName := pod.Spec.NodeName
		if nodeName == "" {
			fmt.Fprintf(out, "   ℹ️  Pod %q is not yet scheduled to a node; skipping node check.\n", pod.Name)
			continue
		}

		res, err := c.nodeMetadataStatus(ctx, nodeCache, nodeName)
		if err != nil {
			return fmt.Errorf("getting node %q for pod %q: %w", nodeName, pod.Name, err)
		}
		if res.notFoundErr != nil {
			if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
				fmt.Fprintf(out, "   ℹ️  Skipping terminated pod %q (phase %s): node %q no longer exists.\n", pod.Name, pod.Status.Phase, nodeName)
				continue
			}
			return fmt.Errorf("getting node %q for pod %q: %w", nodeName, pod.Name, res.notFoundErr)
		}

		checkedPods++
		if !res.enabled {
			missingPods = append(missingPods, fmt.Sprintf("%s (node %s)", pod.Name, nodeName))
		}
	}

	if len(missingPods) > 0 {
		slices.Sort(missingPods)
		return fmt.Errorf(
			"%w: %s %q in namespace %q cannot leverage Workload Identity Federation because the following pod(s) are running on node(s) whose node pool does not have GKE Metadata Server enabled (missing label %q=\"true\"): %s\n\n"+
				"Recommended actions:\n"+
				"  1. If only specific node pools are intended to have GKE Metadata Server enabled, add the following nodeSelector to your workload/pod spec:\n"+
				"       nodeSelector:\n"+
				"         %s: \"true\"\n"+
				"  2. Otherwise, enable GKE Metadata Server (--workload-metadata=GKE_METADATA) on your cluster's node pools",
			errMetadataServerDisabled, workload.kind, workload.name, workload.namespace, gkeMetadataServerLabel, strings.Join(missingPods, ", "), gkeMetadataServerLabel,
		)
	}

	if checkedPods == 0 {
		fmt.Fprintf(out, "   ℹ️  No scheduled pods on existing nodes found for %s %q in namespace %q; skipping node GKE Metadata Server check.\n", workload.kind, workload.name, workload.namespace)
		return nil
	}

	fmt.Fprintf(out, "   ✅ All %d checked pod(s) are running on node(s) with GKE Metadata Server enabled (%s=\"true\").\n", checkedPods, gkeMetadataServerLabel)
	return nil
}

func (c *workloadNodeChecker) nodeMetadataStatus(ctx context.Context, cache map[string]nodeCheckResult, nodeName string) (nodeCheckResult, error) {
	if res, ok := cache[nodeName]; ok {
		return res, nil
	}
	node, err := c.clientset.CoreV1().Nodes().Get(ctx, nodeName, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		res := nodeCheckResult{notFoundErr: err}
		cache[nodeName] = res
		return res, nil
	}
	if err != nil {
		return nodeCheckResult{}, err
	}
	res := nodeCheckResult{enabled: node.Labels[gkeMetadataServerLabel] == "true"}
	cache[nodeName] = res
	return res, nil
}

func (c *workloadNodeChecker) pods(ctx context.Context, workload workloadDetails) ([]corev1.Pod, error) {
	switch workload.kind {
	case "Pod":
		if workload.pod == nil {
			return nil, nil
		}
		return []corev1.Pod{*workload.pod}, nil
	case "Deployment":
		selector, err := labelSelectorString(workload.selector)
		if err != nil {
			return nil, fmt.Errorf("building selector for deployment %q: %w", workload.name, err)
		}
		rsList, err := c.clientset.AppsV1().ReplicaSets(workload.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing replicasets for deployment %q in namespace %q: %w", workload.name, workload.namespace, err)
		}
		var ownedRS []ownerTarget
		for _, rs := range rsList.Items {
			if isOwnedBy(rs.OwnerReferences, "Deployment", workload.name, workload.uid) {
				ownedRS = append(ownedRS, ownerTarget{kind: "ReplicaSet", name: rs.Name, uid: rs.UID})
			}
		}
		if len(ownedRS) == 0 {
			return nil, nil
		}
		podList, err := c.clientset.CoreV1().Pods(workload.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("listing pods for deployment %q in namespace %q: %w", workload.name, workload.namespace, err)
		}
		return filterPodsByOwners(podList.Items, ownedRS), nil
	case "StatefulSet", "DaemonSet", "Job":
		selector, err := labelSelectorString(workload.selector)
		if err != nil {
			return nil, fmt.Errorf("building selector for %s %q: %w", strings.ToLower(workload.kind), workload.name, err)
		}
		podList, err := c.clientset.CoreV1().Pods(workload.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector})
		if err != nil {
			return nil, fmt.Errorf("listing pods for %s %q in namespace %q: %w", strings.ToLower(workload.kind), workload.name, workload.namespace, err)
		}
		return filterPodsByOwners(podList.Items, []ownerTarget{{kind: workload.kind, name: workload.name, uid: workload.uid}}), nil
	case "CronJob":
		jobList, err := c.clientset.BatchV1().Jobs(workload.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing jobs for cronjob %q in namespace %q: %w", workload.name, workload.namespace, err)
		}
		var ownedJobs []ownerTarget
		for _, job := range jobList.Items {
			if isOwnedBy(job.OwnerReferences, "CronJob", workload.name, workload.uid) {
				ownedJobs = append(ownedJobs, ownerTarget{kind: "Job", name: job.Name, uid: job.UID})
			}
		}
		if len(ownedJobs) == 0 {
			return nil, nil
		}
		podList, err := c.clientset.CoreV1().Pods(workload.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("listing pods for cronjob %q in namespace %q: %w", workload.name, workload.namespace, err)
		}
		return filterPodsByOwners(podList.Items, ownedJobs), nil
	default:
		return nil, fmt.Errorf("%w %q", errUnsupportedWorkloadType, workload.kind)
	}
}

func labelSelectorString(sel *metav1.LabelSelector) (string, error) {
	if sel == nil {
		return "", nil
	}
	parsed, err := metav1.LabelSelectorAsSelector(sel)
	if err != nil {
		return "", fmt.Errorf("parsing label selector: %w", err)
	}
	return parsed.String(), nil
}

func filterPodsByOwners(pods []corev1.Pod, owners []ownerTarget) []corev1.Pod {
	var matched []corev1.Pod
	for _, pod := range pods {
		for _, owner := range owners {
			if isOwnedBy(pod.OwnerReferences, owner.kind, owner.name, owner.uid) {
				matched = append(matched, pod)
				break
			}
		}
	}
	return matched
}

func isOwnedBy(refs []metav1.OwnerReference, kind, name string, uid types.UID) bool {
	for _, ref := range refs {
		if ref.Kind != kind {
			continue
		}
		if uid != "" && ref.UID != "" {
			if ref.UID == uid {
				return true
			}
			continue
		}
		if ref.Name == name {
			return true
		}
	}
	return false
}

func init() {
	checkCmd.AddCommand(workloadCmd)
	workloadCmd.Flags().StringVarP(&workloadNamespace, "namespace", "n", "default", "Kubernetes namespace of the workload")
	workloadCmd.Flags().StringVarP(&workloadType, "type", "t", "deployment", "Type of the workload (deployment, statefulset, daemonset, job, cronjob, pod)")
}
