# Node Leaf Cert Expiry Check

This tool checks the expiry of node bootstrap leaf certificates stored in the instance template metadata used by GKE node pools. It parses the certificate from the kube_env metadata key and prints the expiration time for each certificate.

## Usage

```bash
go run cmd/node-leaf-cert-expiry-check/main.go node-leaf-cert-expiry-check --project <PROJECT_ID> --location <LOCATION> --cluster <CLUSTER_NAME>
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
