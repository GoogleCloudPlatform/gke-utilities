# Find expiring cluster certificate authorities across an organization

In order to get an organization wide picture of upcoming cluster certificate authority expirations, you can use Cloud Asset Inventory and BigQuery.

Variables we will use:
* `ORGANIZATION_ID`: The ID of the organization you want to scan.
* `ANALYIS_PROJECT_ID`: The project ID of the project where you will export the Cloud Asset Inventory data for analysis with BigQuery.
* `BQ_DATASET`: The name of the BigQuery dataset that will hold the Cloud Asset Inventory data.
* `BQ_TABLE`: The name of the BigQuery table that will hold the Cloud Asset Inventory data.

# Export Cloud Asset Inventory data to BigQuery

Run the following commands to export a snapshot of all GKE clusters from Cloud Asset Inventory to a BigQuery dataset.  You don't need to do this step if your organization already has a Cloud Asset Inventory dataset for other purposes, as long as that dataset includes data on GKE clusters.

```bash
gcloud asset export \
  --organization="${ORGANIZATION_ID}" \
  --billing-project="${ANALYSIS_PROJECT_ID}" \
  --asset-types=container.googleapis.com/Cluster \
  --content-type=resource \
  --bigquery-table="projects/${ANALYSIS_PROJECT_ID}/datasets/${BQ_DATASET}/tables/${BQ_TABLE}" \
  --output-bigquery-force
```

# Analyze the exported data using BigQuery

1) Open the BigQuery explorer in your analysis project.
2) Copy and paste the SQL script from find-expiring-certificate-authorities.sql into a new query.
3) Scroll to the bottom of the query, and replace the following placeholders
  * `_ANALYSIS_PROJECT_ID_`
  * `_BQ_DATASET_`
  * `_BQ_TABLE_`
4) Execute the query.

The query output will have one or more rows per cluster in your organization.  Each row corresponds to a certificate authority root certificate, with the "not before" and "not after" (expiration) dates.  Most clusters will have one row, but clusters that are currently undergoing credential rotation will have two active certificate authorities, and thus two rows.