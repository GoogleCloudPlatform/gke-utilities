# Node Bootstrap Leaf Cert Expiry Check

This tool checks the expiry of node bootstrap leaf certificates stored in the instance template metadata used by GKE node pools. It parses the certificate from the kube_env metadata key and prints the expiration time for each certificate.

## Before you begin

1. **Install Go**: Ensure you have Go installed. You can download it from [golang.org](https://golang.org/).
2. **Set up Credentials**: Ensure you have Application Default Credentials set up. You can do this by running:
   ```bash
   gcloud auth application-default login
   ```
   The account used must have permissions to read GKE clusters and Compute Engine instance templates in the target project.

## Usage

```bash
go run cmd/node-bootstrap-leaf-cert-expiry-check/main.go node-bootstrap-leaf-cert-expiry-check --project <PROJECT_ID> --location <LOCATION> --cluster <CLUSTER_NAME>
```

### Options

- `--project`: The GCP Project ID where the cluster resides.
- `--location`: The GKE Cluster location (zone or region).
- `--cluster`: The GKE Cluster name.

## How it works

The tool performs the following steps:
1. **Fetches Cluster Details**: Uses the GKE API to get the cluster configuration, including its node pools.
2. **Iterates Node Pools**: For each node pool, it constructs a filter for labels.
3. **Lists Instance Templates**: It fetches the instance templates for the node pool.
4. **Extracts Metadata**: Reads the `kube-env` metadata field from the Instance Template.
5. **Parses Certificates**: Unmarshals the `kube-env` content as YAML and extracts the `TPM_BOOTSTRAP_CERT` and `KUBELET_CERT` values.
6. **Checks Expiry**: Decodes the base64 certificates and prints their expiration timestamps.
