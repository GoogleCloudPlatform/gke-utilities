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
	"bytes"
	"errors"
	"strings"
	"testing"

	"cloud.google.com/go/container/apiv1/containerpb"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

func TestFetchWorkload(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "my-deploy", Namespace: "prod"},
			Spec: appsv1.DeploymentSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{ServiceAccountName: "deploy-ksa"},
				},
			},
		},
		&appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{Name: "default-sa-deploy", Namespace: "prod"},
		},
		&appsv1.StatefulSet{
			ObjectMeta: metav1.ObjectMeta{Name: "my-sts", Namespace: "prod"},
			Spec: appsv1.StatefulSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{ServiceAccountName: "sts-ksa"},
				},
			},
		},
		&appsv1.DaemonSet{
			ObjectMeta: metav1.ObjectMeta{Name: "my-ds", Namespace: "prod"},
			Spec: appsv1.DaemonSetSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{ServiceAccountName: "ds-ksa"},
				},
			},
		},
		&batchv1.Job{
			ObjectMeta: metav1.ObjectMeta{Name: "my-job", Namespace: "prod"},
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{ServiceAccountName: "job-ksa"},
				},
			},
		},
		&batchv1.CronJob{
			ObjectMeta: metav1.ObjectMeta{Name: "my-cj", Namespace: "prod"},
			Spec: batchv1.CronJobSpec{
				JobTemplate: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{ServiceAccountName: "cj-ksa"},
						},
					},
				},
			},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "prod"},
			Spec:       corev1.PodSpec{ServiceAccountName: "pod-ksa"},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "default-sa-pod", Namespace: "prod"},
		},
	)

	tests := []struct {
		name     string
		target   workloadTarget
		wantKind string
		wantKSA  string
		wantErr  error
	}{
		{
			name:     "DeploymentExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-deploy", kind: "deployment"},
			wantKind: "Deployment",
			wantKSA:  "deploy-ksa",
		},
		{
			name:     "DeploymentDefaultKSA",
			target:   workloadTarget{namespace: "prod", name: "default-sa-deploy", kind: "deploy"},
			wantKind: "Deployment",
			wantKSA:  "default",
		},
		{
			name:     "StatefulSetExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-sts", kind: "sts"},
			wantKind: "StatefulSet",
			wantKSA:  "sts-ksa",
		},
		{
			name:     "DaemonSetExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-ds", kind: "ds"},
			wantKind: "DaemonSet",
			wantKSA:  "ds-ksa",
		},
		{
			name:     "JobExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-job", kind: "job"},
			wantKind: "Job",
			wantKSA:  "job-ksa",
		},
		{
			name:     "CronJobExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-cj", kind: "cronjob"},
			wantKind: "CronJob",
			wantKSA:  "cj-ksa",
		},
		{
			name:     "PodExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-pod", kind: "pod"},
			wantKind: "Pod",
			wantKSA:  "pod-ksa",
		},
		{
			name:     "PodShortAliasExplicitKSA",
			target:   workloadTarget{namespace: "prod", name: "my-pod", kind: "po"},
			wantKind: "Pod",
			wantKSA:  "pod-ksa",
		},
		{
			name:     "PodDefaultKSA",
			target:   workloadTarget{namespace: "prod", name: "default-sa-pod", kind: "pod"},
			wantKind: "Pod",
			wantKSA:  "default",
		},
		{
			name:     "PodCaseInsensitive",
			target:   workloadTarget{namespace: "prod", name: "my-pod", kind: "Pod"},
			wantKind: "Pod",
			wantKSA:  "pod-ksa",
		},
		{
			name:    "UnsupportedWorkloadType",
			target:  workloadTarget{namespace: "prod", name: "my-deploy", kind: "unknown"},
			wantErr: errUnsupportedWorkloadType,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := fetchWorkload(t.Context(), clientset, tc.target)
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("fetchWorkload(%+v) err = %v, want %v", tc.target, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("fetchWorkload(%+v) unexpected error: %v", tc.target, err)
			}
			if got.kind != tc.wantKind {
				t.Errorf("fetchWorkload(%+v).kind = %q, want %q", tc.target, got.kind, tc.wantKind)
			}
			if got.serviceAccountName != tc.wantKSA {
				t.Errorf("fetchWorkload(%+v).serviceAccountName = %q, want %q", tc.target, got.serviceAccountName, tc.wantKSA)
			}
		})
	}
}

func TestFetchWorkloadPodNotFound(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	target := workloadTarget{namespace: "prod", name: "missing-pod", kind: "pod"}
	got, err := fetchWorkload(t.Context(), clientset, target)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("fetchWorkload(%+v) = (%+v, %v), want NotFound error", target, got, err)
	}
}

func TestWorkloadNodeCheckerCheckMetadataServer(t *testing.T) {
	standardCluster := &containerpb.Cluster{
		Name: "standard-cluster",
	}
	autopilotCluster := &containerpb.Cluster{
		Name:      "autopilot-cluster",
		Autopilot: &containerpb.Autopilot{Enabled: true},
	}
	enabledNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-wif-enabled",
			Labels: map[string]string{gkeMetadataServerLabel: "true"},
		},
	}
	disabledNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name: "node-no-wif",
		},
	}
	labelFalseNode := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-wif-false",
			Labels: map[string]string{gkeMetadataServerLabel: "false"},
		},
	}

	tests := []struct {
		name               string
		cluster            *containerpb.Cluster
		target             workloadTarget
		objects            []runtime.Object
		wantErr            error
		wantErrSubstrs     []string
		wantErrNotContains []string
		wantOutSubstrs     []string
		wantNodeGets       int
	}{
		{
			name:    "AutopilotClusterSkippedWithoutK8sNodeLookup",
			cluster: autopilotCluster,
			target:  workloadTarget{namespace: "prod", name: "my-pod", kind: "pod"},
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "prod"},
					Spec:       corev1.PodSpec{NodeName: "node-no-wif"},
				},
				disabledNode,
			},
			wantOutSubstrs: []string{"Skipping node GKE Metadata Server check", "Autopilot"},
			wantNodeGets:   0,
		},
		{
			name:    "PodOnEnabledNode",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "my-pod", kind: "pod"},
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "prod"},
					Spec:       corev1.PodSpec{NodeName: "node-wif-enabled"},
				},
				enabledNode,
			},
			wantOutSubstrs: []string{"All 1 checked pod(s) are running on node(s) with GKE Metadata Server enabled"},
			wantNodeGets:   1,
		},
		{
			name:    "PodOnNodeMissingMetadataServerLabel",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "my-pod", kind: "pod"},
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "prod"},
					Spec:       corev1.PodSpec{NodeName: "node-no-wif"},
				},
				disabledNode,
			},
			wantErr: errMetadataServerDisabled,
			wantErrSubstrs: []string{
				"cannot leverage Workload Identity Federation",
				"my-pod (node node-no-wif)",
				"nodeSelector:",
				`iam.gke.io/gke-metadata-server-enabled: "true"`,
				"enable GKE Metadata Server",
			},
			wantNodeGets: 1,
		},
		{
			name:    "PodOnNodeWithLabelFalse",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "my-pod", kind: "pod"},
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "my-pod", Namespace: "prod"},
					Spec:       corev1.PodSpec{NodeName: "node-wif-false"},
				},
				labelFalseNode,
			},
			wantErr:        errMetadataServerDisabled,
			wantErrSubstrs: []string{"my-pod (node node-wif-false)"},
			wantNodeGets:   1,
		},
		{
			name:    "DeploymentDeduplicatesNodeLookupsWhenAllPodsOnEnabledNode",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "web", kind: "deployment"},
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: types.UID("deploy-1")},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-rs",
						Namespace: "prod",
						UID:       types.UID("rs-1"),
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Deployment", Name: "web", UID: types.UID("deploy-1")},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-pod-1",
						Namespace: "prod",
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "web-rs", UID: types.UID("rs-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-enabled"},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-pod-2",
						Namespace: "prod",
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "web-rs", UID: types.UID("rs-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-enabled"},
				},
				enabledNode,
			},
			wantOutSubstrs: []string{"All 2 checked pod(s) are running on node(s) with GKE Metadata Server enabled"},
			wantNodeGets:   1,
		},
		{
			name:    "DeploymentMixedNodesIgnoresUnrelatedOverlappingDeployment",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "web", kind: "deployment"},
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "prod", UID: types.UID("deploy-1")},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "web"}},
					},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-rs",
						Namespace: "prod",
						UID:       types.UID("rs-1"),
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Deployment", Name: "web", UID: types.UID("deploy-1")},
						},
					},
				},
				&appsv1.ReplicaSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-web-rs",
						Namespace: "prod",
						UID:       types.UID("rs-other"),
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Deployment", Name: "web-canary", UID: types.UID("deploy-other")},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-good",
						Namespace: "prod",
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "web-rs", UID: types.UID("rs-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-enabled"},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "web-bad",
						Namespace: "prod",
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "web-rs", UID: types.UID("rs-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-no-wif"},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "canary-bad",
						Namespace: "prod",
						Labels:    map[string]string{"app": "web"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "ReplicaSet", Name: "other-web-rs", UID: types.UID("rs-other")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-false"},
				},
				enabledNode,
				disabledNode,
				labelFalseNode,
			},
			wantErr:            errMetadataServerDisabled,
			wantErrSubstrs:     []string{"web-bad (node node-no-wif)"},
			wantErrNotContains: []string{"canary-bad", "web-good"},
			wantNodeGets:       2,
		},
		{
			name:    "StatefulSetPodOnDisabledNode",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "db", kind: "statefulset"},
			objects: []runtime.Object{
				&appsv1.StatefulSet{
					ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "prod", UID: types.UID("sts-1")},
					Spec: appsv1.StatefulSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "db"}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "db-0",
						Namespace: "prod",
						Labels:    map[string]string{"app": "db"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "StatefulSet", Name: "db", UID: types.UID("sts-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-no-wif"},
				},
				disabledNode,
			},
			wantErr:        errMetadataServerDisabled,
			wantErrSubstrs: []string{"db-0 (node node-no-wif)"},
			wantNodeGets:   1,
		},
		{
			name:    "DaemonSetAllPodsOnEnabledNodes",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "agent", kind: "daemonset"},
			objects: []runtime.Object{
				&appsv1.DaemonSet{
					ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "prod", UID: types.UID("ds-1")},
					Spec: appsv1.DaemonSetSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "agent"}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "agent-abc",
						Namespace: "prod",
						Labels:    map[string]string{"app": "agent"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "DaemonSet", Name: "agent", UID: types.UID("ds-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-enabled"},
				},
				enabledNode,
			},
			wantOutSubstrs: []string{"All 1 checked pod(s) are running on node(s) with GKE Metadata Server enabled"},
			wantNodeGets:   1,
		},
		{
			name:    "JobPodOnDisabledNode",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "batch", kind: "job"},
			objects: []runtime.Object{
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "batch", Namespace: "prod", UID: types.UID("job-1")},
					Spec: batchv1.JobSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"job-name": "batch"}},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "batch-xyz",
						Namespace: "prod",
						Labels:    map[string]string{"job-name": "batch"},
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Job", Name: "batch", UID: types.UID("job-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-no-wif"},
				},
				disabledNode,
			},
			wantErr:        errMetadataServerDisabled,
			wantErrSubstrs: []string{"batch-xyz (node node-no-wif)"},
			wantNodeGets:   1,
		},
		{
			name:    "CronJobPodOnDisabledNodeIgnoresUnrelatedJob",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "nightly", kind: "cronjob"},
			objects: []runtime.Object{
				&batchv1.CronJob{
					ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "prod", UID: types.UID("cj-1")},
				},
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nightly-100",
						Namespace: "prod",
						UID:       types.UID("cj-job-1"),
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "CronJob", Name: "nightly", UID: types.UID("cj-1")},
						},
					},
				},
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{Name: "other-job", Namespace: "prod", UID: types.UID("other-job-uid")},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nightly-100-pod",
						Namespace: "prod",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Job", Name: "nightly-100", UID: types.UID("cj-job-1")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-no-wif"},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "other-job-pod",
						Namespace: "prod",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Job", Name: "other-job", UID: types.UID("other-job-uid")},
						},
					},
					Spec: corev1.PodSpec{NodeName: "node-wif-false"},
				},
				disabledNode,
				labelFalseNode,
			},
			wantErr:            errMetadataServerDisabled,
			wantErrSubstrs:     []string{"nightly-100-pod (node node-no-wif)"},
			wantErrNotContains: []string{"other-job-pod"},
			wantNodeGets:       1,
		},
		{
			name:    "TerminatedPodOnDeletedNodeSkipped",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "nightly", kind: "cronjob"},
			objects: []runtime.Object{
				&batchv1.CronJob{
					ObjectMeta: metav1.ObjectMeta{Name: "nightly", Namespace: "prod", UID: types.UID("cj-1")},
				},
				&batchv1.Job{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nightly-old",
						Namespace: "prod",
						UID:       types.UID("cj-job-old"),
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "CronJob", Name: "nightly", UID: types.UID("cj-1")},
						},
					},
				},
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "nightly-old-pod",
						Namespace: "prod",
						OwnerReferences: []metav1.OwnerReference{
							{Kind: "Job", Name: "nightly-old", UID: types.UID("cj-job-old")},
						},
					},
					Spec:   corev1.PodSpec{NodeName: "deleted-node"},
					Status: corev1.PodStatus{Phase: corev1.PodSucceeded},
				},
			},
			wantOutSubstrs: []string{"Skipping terminated pod", "No scheduled pods on existing nodes found"},
			wantNodeGets:   1,
		},
		{
			name:    "PendingUnscheduledPodSkipped",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "pending-pod", kind: "pod"},
			objects: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{Name: "pending-pod", Namespace: "prod"},
					Status:     corev1.PodStatus{Phase: corev1.PodPending},
				},
			},
			wantOutSubstrs: []string{"not yet scheduled to a node", "No scheduled pods on existing nodes found"},
			wantNodeGets:   0,
		},
		{
			name:    "WorkloadWithNoPodsSkipped",
			cluster: standardCluster,
			target:  workloadTarget{namespace: "prod", name: "scaled-zero", kind: "deployment"},
			objects: []runtime.Object{
				&appsv1.Deployment{
					ObjectMeta: metav1.ObjectMeta{Name: "scaled-zero", Namespace: "prod", UID: types.UID("deploy-0")},
					Spec: appsv1.DeploymentSpec{
						Selector: &metav1.LabelSelector{MatchLabels: map[string]string{"app": "scaled-zero"}},
					},
				},
			},
			wantOutSubstrs: []string{"No pods found"},
			wantNodeGets:   0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clientset := fake.NewSimpleClientset(tc.objects...)
			workload, err := fetchWorkload(t.Context(), clientset, tc.target)
			if err != nil {
				t.Fatalf("fetchWorkload(%+v) unexpected error: %v", tc.target, err)
			}
			clientset.ClearActions()

			var out bytes.Buffer
			checker := &workloadNodeChecker{
				clientset: clientset,
				out:       &out,
			}
			err = checker.checkMetadataServer(t.Context(), tc.cluster, workload)

			gotNodeGets := 0
			for _, action := range clientset.Actions() {
				if action.GetVerb() == "get" && action.GetResource().Resource == "nodes" {
					gotNodeGets++
				}
			}
			if gotNodeGets != tc.wantNodeGets {
				t.Errorf("checkMetadataServer(%+v) node get count = %d, want %d", tc.target, gotNodeGets, tc.wantNodeGets)
			}

			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("checkMetadataServer(%+v) err = %v, want %v", tc.target, err, tc.wantErr)
				}
				for _, sub := range tc.wantErrSubstrs {
					if !strings.Contains(err.Error(), sub) {
						t.Errorf("checkMetadataServer(%+v) error = %q, want substring %q", tc.target, err.Error(), sub)
					}
				}
				for _, forbidden := range tc.wantErrNotContains {
					if strings.Contains(err.Error(), forbidden) {
						t.Errorf("checkMetadataServer(%+v) error = %q, unexpectedly contains %q", tc.target, err.Error(), forbidden)
					}
				}
				return
			}
			if err != nil {
				t.Fatalf("checkMetadataServer(%+v) unexpected error: %v", tc.target, err)
			}
			for _, sub := range tc.wantOutSubstrs {
				if !strings.Contains(out.String(), sub) {
					t.Errorf("checkMetadataServer(%+v) output = %q, want substring %q", tc.target, out.String(), sub)
				}
			}
		})
	}
}

func TestWorkloadNodeCheckerRunningPodMissingNodeReturnsNotFound(t *testing.T) {
	clientset := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: "running-pod", Namespace: "prod"},
			Spec:       corev1.PodSpec{NodeName: "missing-node"},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)
	target := workloadTarget{namespace: "prod", name: "running-pod", kind: "pod"}
	workload, err := fetchWorkload(t.Context(), clientset, target)
	if err != nil {
		t.Fatalf("fetchWorkload(%+v) unexpected error: %v", target, err)
	}

	var out bytes.Buffer
	checker := &workloadNodeChecker{
		clientset: clientset,
		out:       &out,
	}
	err = checker.checkMetadataServer(t.Context(), &containerpb.Cluster{Name: "std"}, workload)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("checkMetadataServer(%+v) err = %v, want NotFound error", target, err)
	}
}
