# GKE Workload Identity Federation Troubleshooter

`gke-wif-troubleshooter` is a command-line tool designed to diagnose and resolve common configuration issues with Workload Identity Federation on Google Kubernetes Engine (GKE).

It helps you verify that your GKE clusters, Kubernetes Service Accounts (KSAs), Google Service Accounts (GSAs), IAM bindings, and node pools are correctly configured to allow your GKE workloads to securely access Google Cloud services.

## Prerequisites

Before using this tool, ensure you have the following:

1.  **Go:** Version 1.26 or later installed.
2.  **Google Cloud SDK (`gcloud`):** Installed and authenticated. You will need Application Default Credentials configured:
    ```bash
    gcloud auth login
    gcloud auth application-default login
    ```
3.  **GKE Auth Plugin:** The tool uses `gke-gcloud-auth-plugin` to authenticate with your GKE cluster unless `--local-kubeconfig` is specified.

## Installation

Install directly using `go install`:

```bash
go install github.com/GoogleCloudPlatform/gke-utilities/gke-wif-troubleshooter@latest
```

Or build from source:

```bash
git clone https://github.com/GoogleCloudPlatform/gke-utilities.git
cd gke-utilities/gke-wif-troubleshooter
go build .
```

## Usage

The tool provides commands to check the Workload Identity Federation configuration for Kubernetes Service Accounts and workloads.

All `check` subcommands require the GKE cluster details:
*   `--project`: Your GCP Project ID.
*   `--location`: The region or zone of your GKE cluster.
*   `--cluster`: The name of your GKE cluster.
*   `--local-kubeconfig` *(optional)*: Use `~/.kube/config` instead of building a cluster client from the GKE API response.

### Check a Kubernetes Service Account (KSA)

This command analyzes a specific KSA to verify its Workload Identity Federation setup.

```bash
gke-wif-troubleshooter check ksa <KSA_NAME> \
  --namespace <NAMESPACE> \
  --project <PROJECT_ID> \
  --location <CLUSTER_LOCATION> \
  --cluster <CLUSTER_NAME>
```

**Example:**

```bash
gke-wif-troubleshooter check ksa my-app-ksa \
  --namespace my-app-ns \
  --project my-gcp-project \
  --location us-central1 \
  --cluster my-gke-cluster
```

### Check a Kubernetes Workload

This command analyzes a Kubernetes workload (such as a Pod, Deployment, StatefulSet, DaemonSet, Job, or CronJob) to find its KSA, perform all KSA/IAM checks, and validate the node GKE Metadata Server configuration.

```bash
gke-wif-troubleshooter check workload <WORKLOAD_NAME> \
  --type <WORKLOAD_TYPE> \
  --namespace <NAMESPACE> \
  --project <PROJECT_ID> \
  --location <CLUSTER_LOCATION> \
  --cluster <CLUSTER_NAME>
```

Supported workload types: `deployment` (`deploy`), `statefulset` (`sts`), `daemonset` (`ds`), `job`, `cronjob` (`cj`), `pod` (`po`).

**Example:**

```bash
gke-wif-troubleshooter check workload my-deployment \
  --type deployment \
  --namespace my-app-ns \
  --project my-gcp-project \
  --location us-central1 \
  --cluster my-gke-cluster
```

## What It Checks

The troubleshooter performs a series of validations:

1.  **Cluster Configuration:**
    *   Verifies that Workload Identity Federation is enabled on the specified GKE cluster.

2.  **Kubernetes Service Account (KSA):**
    *   Confirms that the KSA exists in the specified namespace.
    *   Checks for the `iam.gke.io/gcp-service-account` annotation, which links the KSA to a Google Service Account (GSA).

3.  **IAM Bindings:**
    *   **If the KSA is annotated:** Verifies that the GSA has an IAM policy binding with the `roles/iam.workloadIdentityUser` role for the KSA's principal.
    *   **If the KSA is NOT annotated:** Checks if the KSA's principal has been granted IAM roles directly at the project level.

4.  **Node GKE Metadata Server (`check workload` on Standard Clusters):**
    *   Inspects all pods belonging to the workload (or the specified pod) and verifies that each scheduled node has the `iam.gke.io/gke-metadata-server-enabled: "true"` label.
    *   Skipped automatically for GKE Autopilot clusters.

The tool provides clear success messages or actionable error messages to help you fix any detected issues.

## Usage Attribution & Privacy

`gke-wif-troubleshooter` does **not** send separate telemetry or phone-home analytics. It includes a static, versioned `User-Agent` header (`gke-wif-troubleshooter/<version>`) on the Google Cloud and Kubernetes API requests it already makes, containing no PII, project IDs, cluster names, or user identifiers.

When building release binaries from source, you can override the version via `-ldflags`:

```bash
go build -ldflags "-X github.com/GoogleCloudPlatform/gke-utilities/gke-wif-troubleshooter/cmd.version=v0.1.0" .
```
