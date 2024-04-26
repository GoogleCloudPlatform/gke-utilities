# gke-utilities

A helper package for case-by-case support tooling for GKE.

## gke-identity-service-migrator

Today, Google Cloud has two services providing similar functionality allowing external identity providers to authenticate in order for users to access a GKE cluster:
1. [Identity Service for GKE](https://cloud.google.com/kubernetes-engine/docs/how-to/oidc)
2. [Google Cloud IAM Workforce Identity Federation](https://cloud.google.com/iam/docs/workforce-identity-federation)

We are encouraging our users to move to Workforce Identity Federation as a holistic solution for your Google Cloud deployments, with a unified product approach.

Use [gke-identity-service-migrator](https://github.com/GoogleCloudPlatform/gke-utilities/tree/main/cmd/gke-identity-service-migrator) to identify federated users and groups for RoleBindings and ClusterRoleBindings, and translate them to the Workforce Identity Federation syntax. Be sure to test on a non-production cluster to confirm intended behaviour.

We encourage the following prerequisite steps are completed prior to the migration steps: 
1. Confirm your external identity provider is set up
2. Confirm the existing Identity Service for GKE configuration.

Once the prerequisites are completed, migrate your Identity Service for GKE to Google Cloud Workforce Identity Federation with the following steps: 
1. Configure and test Google Cloud Workforce Identity Federation
2. Install [gke-identity-service-migrator](https://github.com/GoogleCloudPlatform/gke-utilities/tree/main/cmd/gke-identity-service-migrator) migration tooling:  `go install github.com/GoogleCloudPlatform/gke-utilities/cmd/gke-identity-service-migrator@latest`
3. Use [gke-identity-service-migrator](https://github.com/GoogleCloudPlatform/gke-utilities/tree/main/cmd/gke-identity-service-migrator) to identify RoleBindings and ClusterRoleBindings that refer to federated users and groups
4. Use [gke-identity-service-migrator](https://github.com/GoogleCloudPlatform/gke-utilities/tree/main/cmd/gke-identity-service-migrator) to create transformed copies of RoleBindings and ClusterRoleBindings with Workforce Identity Federation syntax
5. Apply the translated configs to your cluster
6. Test user access when logged in via Workforce Identity Federation
7. Clean up old RoleBinding and ClusterRoleBinding objects
8. Disable Identity Service for GKE.

For more details on the migration guide, please contact your Google team. 

If you encounter any issues with the tool, please raise a GitHub issue for this repo.
