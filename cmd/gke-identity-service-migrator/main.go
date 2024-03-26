// Copyright 2024 Google LLC
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

// Command gke-identity-service-migrator helps you migrate a federated identity
// setup in GKE from using Identity Service for GKE to GCP-wide federation using
// Workforce Identity Federation.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/subcommands"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/cli-runtime/pkg/printers"
	"k8s.io/cli-runtime/pkg/resource"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

func main() {
	subcommands.Register(subcommands.HelpCommand(), "")
	subcommands.Register(subcommands.FlagsCommand(), "")
	subcommands.Register(subcommands.CommandsCommand(), "")

	subcommands.Register(&FindCRBCommand{}, "")
	subcommands.Register(&FindRBCommand{}, "")

	subcommands.Register(&RewriteCRBCommand{}, "")
	subcommands.Register(&RewriteRBCommand{}, "")

	flag.Parse()
	ctx := context.Background()
	os.Exit(int(subcommands.Execute(ctx)))
}

// FindCRBCommand searches for and prints all ClusterRoleBindings that appear to
// refer to users and groups federated using Identity Service for GKE.
type FindCRBCommand struct {
	kubeConfig string

	userIncludePrefix   string
	userIncludeSuffix   string
	groupsIncludePrefix string
	groupsExcludeSuffix string
}

var _ subcommands.Command = (*FindCRBCommand)(nil)

func (*FindCRBCommand) Name() string {
	return "find-clusterrolebindings"
}

func (*FindCRBCommand) Synopsis() string {
	return "Finds all ClusterRoleBinding objects in the cluster that appear to refer to federated users or groups"
}

func (*FindCRBCommand) Usage() string {
	return `find-clusterrolebindings [--kubeconfig] [--user-email-suffix] :
  Identify ClusterRoleBinding objects in the cluster that appear to refer to federated users or groups.
  Users are recognized by an email suffix.
  Groups are assumed to be federated unless they have a system: prefix
`
}

func (c *FindCRBCommand) SetFlags(f *flag.FlagSet) {
	kubeConfigDefault := ""
	if home := homedir.HomeDir(); home != "" {
		kubeConfigDefault = filepath.Join(home, ".kube", "config")
	}

	f.StringVar(&c.kubeConfig, "kubeconfig", kubeConfigDefault, "absolute path to the kubeconfig file")
	f.StringVar(&c.userIncludePrefix, "user-include-prefix", "", "Prefix for recognizing federated user identities.  Will be stripped from the translated user name.")
	f.StringVar(&c.userIncludeSuffix, "user-include-suffix", "", "Suffix for recognizing federated user identities.  Typically your organization's domain name.")
	f.StringVar(&c.groupsIncludePrefix, "groups-include-prefix", "", "Prefix for recognizing federated group names  Will be stripped from the translated user name.")
	f.StringVar(&c.groupsExcludeSuffix, "groups-exclude-suffix", "", "Suffix for excluding federated group names.  Use this to filter out groups introduced by Google Groups for RBAC.")
}

func (c *FindCRBCommand) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.userIncludeSuffix == "" {
		log.Printf("Error: --user-include-suffix must be specified.")
		return subcommands.ExitFailure
	}

	rec := &subjectRecognizer{
		userIncludePrefix:   c.userIncludePrefix,
		userIncludeSuffix:   c.userIncludeSuffix,
		groupsIncludePrefix: c.groupsIncludePrefix,
		groupsExcludeSuffix: c.groupsExcludeSuffix,
	}

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", c.kubeConfig)
	if err != nil {
		log.Printf("Error while initializing Kubernetes REST config: %v", err)
		return subcommands.ExitFailure
	}

	kc, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("Error while initializing Kubernetes client: %v", err)
		return subcommands.ExitFailure
	}

	federatedCRBs := &rbacv1.ClusterRoleBindingList{}

	continueToken := ""
	for {
		crbs, err := kc.RbacV1().ClusterRoleBindings().List(ctx, metav1.ListOptions{Continue: continueToken, Limit: 500})
		if err != nil {
			log.Printf("Error while listing ClusterRoleBindings: %v", err)
			return subcommands.ExitFailure
		}

		for _, crb := range crbs.Items {
			federated := false
			for _, sub := range crb.Subjects {
				if _, ok := rec.GetFederatedUser(sub); ok {
					federated = true
					continue
				}
				if _, ok := rec.GetFederatedGroup(sub); ok {
					federated = true
					continue
				}
			}

			if federated {
				federatedCRBs.Items = append(federatedCRBs.Items, crb)
			}
		}

		if crbs.Continue == "" {
			break
		}

		continueToken = crbs.Continue
	}

	printr := printers.NewTypeSetter(scheme.Scheme).ToPrinter(&printers.YAMLPrinter{})
	if err := printr.PrintObj(federatedCRBs, os.Stdout); err != nil {
		log.Printf("Error while printing identified federated ClusterRoleBindings: %v", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

// RewriteCRBCommand reads a list of ClusterRoleBinding objects identified via
// FindCRBCommand, edits them to refer to users and groups federated via
// Workforce Identity Federation, and writes the result to stdout.
//
// The result is suitable for applying via `kubectl apply`
type RewriteCRBCommand struct {
	userIncludePrefix   string
	userIncludeSuffix   string
	groupsIncludePrefix string
	groupsExcludeSuffix string

	workforcePoolName string
}

var _ subcommands.Command = (*RewriteCRBCommand)(nil)

func (*RewriteCRBCommand) Name() string {
	return "rewrite-clusterrolebindings"
}

func (*RewriteCRBCommand) Synopsis() string {
	return "Reads a ClusterRoleBindingList from stdin and outputs a migrated copy on stdout"
}

func (*RewriteCRBCommand) Usage() string {
	return `rewrite-clusterrolebindings [--kubeconfig=] [--user-email-suffix=] :
  Read a ClusterRoleBindingList produced by find-clusterrolebindings on stdin.
  Translate
`
}

func (c *RewriteCRBCommand) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.userIncludePrefix, "user-include-prefix", "", "Prefix for recognizing federated user identities.  Will be stripped from the translated user name.")
	f.StringVar(&c.userIncludeSuffix, "user-include-suffix", "", "Suffix for recognizing federated user identities.  Typically your organization's domain name.")
	f.StringVar(&c.groupsIncludePrefix, "groups-include-prefix", "", "Prefix for recognizing federated group names  Will be stripped from the translated user name.")
	f.StringVar(&c.groupsExcludeSuffix, "groups-exclude-suffix", "", "Suffix for excluding federated group names.  Use this to filter out groups introduced by Google Groups for RBAC.")

	f.StringVar(&c.workforcePoolName, "workforce-pool-name", "", "The name of the Workforce Identity Pool being used to federate principals and groups into GCP.")
}

func (c *RewriteCRBCommand) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.userIncludeSuffix == "" {
		log.Printf("Error: --user-email-suffix must be specified.")
		return subcommands.ExitFailure
	}
	if c.workforcePoolName == "" {
		log.Printf("Error: --workforce-pool-name must be specified.")
		return subcommands.ExitFailure
	}

	rec := &subjectRecognizer{
		userIncludePrefix:   c.userIncludePrefix,
		userIncludeSuffix:   c.userIncludeSuffix,
		groupsIncludePrefix: c.groupsIncludePrefix,
		groupsExcludeSuffix: c.groupsExcludeSuffix,
		workforcePoolName:   c.workforcePoolName,
	}

	inputCRBs := rbacv1.ClusterRoleBindingList{}
	builder := resource.NewLocalBuilder()
	_ = builder.
		WithScheme(scheme.Scheme, scheme.Scheme.PrioritizedVersionsAllGroups()...).
		DefaultNamespace().
		FilenameParam(
			false,
			&resource.FilenameOptions{Filenames: []string{"-"}}, // Read from stdin
		).
		Do().
		Visit(func(info *resource.Info, _ error) error {
			crbList, ok := info.Object.(*rbacv1.ClusterRoleBindingList)
			if !ok {
				return fmt.Errorf("%v is not a ClusterRoleBindingList", info.Object)
			}
			inputCRBs = *crbList
			return nil
		})

	rewrittenCRBs := &rbacv1.ClusterRoleBindingList{}
	for _, crbIn := range inputCRBs.Items {
		crbOut := crbIn.DeepCopy()

		// Blank out some fields that we don't want to copy over.
		delete(crbOut.ObjectMeta.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
		crbOut.ObjectMeta.CreationTimestamp.Reset()
		crbOut.ObjectMeta.ManagedFields = []metav1.ManagedFieldsEntry{}
		crbOut.ObjectMeta.ResourceVersion = ""
		crbOut.ObjectMeta.UID = types.UID("")

		// Give the object a new name.
		crbOut.ObjectMeta.Name = crbIn.ObjectMeta.Name + "-wfidf"

		crbOut.Subjects = []rbacv1.Subject{}
		for _, sub := range crbIn.Subjects {
			crbOut.Subjects = append(crbOut.Subjects, rec.MigrateSubject(sub))
		}

		rewrittenCRBs.Items = append(rewrittenCRBs.Items, *crbOut)
	}

	printr := printers.NewTypeSetter(scheme.Scheme).ToPrinter(&printers.YAMLPrinter{})
	if err := printr.PrintObj(rewrittenCRBs, os.Stdout); err != nil {
		log.Printf("Error while printing rewritten ClusterRoleBindings: %v", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

// FindRBCommand searches for and prints all RoleBindings that appear to
// refer to users and groups federated using Identity Service for GKE.
type FindRBCommand struct {
	kubeConfig          string
	userIncludePrefix   string
	userIncludeSuffix   string
	groupsIncludePrefix string
	groupsExcludeSuffix string
}

var _ subcommands.Command = (*FindRBCommand)(nil)

func (*FindRBCommand) Name() string {
	return "find-rolebindings"
}

func (*FindRBCommand) Synopsis() string {
	return "Finds all RoleBinding objects in the cluster that appear to refer to federated users or groups"
}

func (*FindRBCommand) Usage() string {
	return `find-rolebindings [--kubeconfig] [--user-email-suffix] :
  Identify RoleBinding objects in the cluster that appear to refer to federated users or groups.
  Users are recognized by an email suffix.
  Groups are assumed to be federated unless they have a system: prefix
`
}

func (c *FindRBCommand) SetFlags(f *flag.FlagSet) {
	kubeConfigDefault := ""
	if home := homedir.HomeDir(); home != "" {
		kubeConfigDefault = filepath.Join(home, ".kube", "config")
	}

	f.StringVar(&c.kubeConfig, "kubeconfig", kubeConfigDefault, "absolute path to the kubeconfig file")
	f.StringVar(&c.userIncludePrefix, "user-include-prefix", "", "Prefix for recognizing federated user identities.  Will be stripped from the translated user name.")
	f.StringVar(&c.userIncludeSuffix, "user-include-suffix", "", "Suffix for recognizing federated user identities.  Typically your organization's domain name.")
	f.StringVar(&c.groupsIncludePrefix, "groups-include-prefix", "", "Prefix for recognizing federated group names  Will be stripped from the translated user name.")
	f.StringVar(&c.groupsExcludeSuffix, "groups-exclude-suffix", "", "Suffix for excluding federated group names.  Use this to filter out groups introduced by Google Groups for RBAC.")
}

func (c *FindRBCommand) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.userIncludeSuffix == "" {
		log.Printf("Error: --user-email-suffix must be specified.")
		return subcommands.ExitFailure
	}

	rec := &subjectRecognizer{
		userIncludePrefix:   c.userIncludePrefix,
		userIncludeSuffix:   c.userIncludeSuffix,
		groupsIncludePrefix: c.groupsIncludePrefix,
		groupsExcludeSuffix: c.groupsExcludeSuffix,
	}

	// use the current context in kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", c.kubeConfig)
	if err != nil {
		log.Printf("Error while initializing Kubernetes REST config: %v", err)
		return subcommands.ExitFailure
	}

	kc, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Printf("Error while initializing Kubernetes client: %v", err)
		return subcommands.ExitFailure
	}

	federatedRBs := &rbacv1.RoleBindingList{}

	continueToken := ""
	for {
		crbs, err := kc.RbacV1().RoleBindings(metav1.NamespaceAll).List(ctx, metav1.ListOptions{Continue: continueToken, Limit: 500})
		if err != nil {
			log.Printf("Error while listing RoleBindings: %v", err)
			return subcommands.ExitFailure
		}

		for _, crb := range crbs.Items {
			federated := false
			for _, sub := range crb.Subjects {
				if _, ok := rec.GetFederatedUser(sub); ok {
					federated = true
					continue
				}
				if _, ok := rec.GetFederatedGroup(sub); ok {
					federated = true
					continue
				}
			}

			if federated {
				federatedRBs.Items = append(federatedRBs.Items, crb)
			}
		}

		if crbs.Continue == "" {
			break
		}

		continueToken = crbs.Continue
	}

	printr := printers.NewTypeSetter(scheme.Scheme).ToPrinter(&printers.YAMLPrinter{})
	if err := printr.PrintObj(federatedRBs, os.Stdout); err != nil {
		log.Printf("Error while printing identified federated ClusterRoleBindings: %v", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

// RewriteRBCommand reads a list of RoleBinding objects identified via
// FindRBCommand, edits them to refer to users and groups federated via
// Workforce Identity Federation, and writes the result to stdout.
//
// The result is suitable for applying via `kubectl apply`
type RewriteRBCommand struct {
	userIncludePrefix   string
	userIncludeSuffix   string
	groupsIncludePrefix string
	groupsExcludeSuffix string

	workforcePoolName string
}

var _ subcommands.Command = (*RewriteRBCommand)(nil)

func (*RewriteRBCommand) Name() string {
	return "rewrite-rolebindings"
}

func (*RewriteRBCommand) Synopsis() string {
	return "Reads a RoleBindingList from stdin and outputs a migrated copy on stdout"
}

func (*RewriteRBCommand) Usage() string {
	return `rewrite-rolebindings [--kubeconfig=] [--user-email-suffix=] :
  Read a RoleBindingList produced by find-rolebindings on stdin.
  Translate
`
}

func (c *RewriteRBCommand) SetFlags(f *flag.FlagSet) {
	f.StringVar(&c.userIncludePrefix, "user-include-prefix", "", "Prefix for recognizing federated user identities.  Will be stripped from the translated user name.")
	f.StringVar(&c.userIncludeSuffix, "user-include-suffix", "", "Suffix for recognizing federated user identities.  Typically your organization's domain name.")
	f.StringVar(&c.groupsIncludePrefix, "groups-include-prefix", "", "Prefix for recognizing federated group names  Will be stripped from the translated user name.")
	f.StringVar(&c.groupsExcludeSuffix, "groups-exclude-suffix", "", "Suffix for excluding federated group names.  Use this to filter out groups introduced by Google Groups for RBAC.")
	f.StringVar(&c.workforcePoolName, "workforce-pool-name", "", "The name of the Workforce Identity Pool being used to federate principals and groups into GCP.")
}

func (c *RewriteRBCommand) Execute(ctx context.Context, f *flag.FlagSet, _ ...interface{}) subcommands.ExitStatus {
	if c.userIncludeSuffix == "" {
		log.Printf("Error: --user-email-suffix must be specified.")
		return subcommands.ExitFailure
	}
	if c.workforcePoolName == "" {
		log.Printf("Error: --workforce-pool-name must be specified.")
		return subcommands.ExitFailure
	}

	rec := &subjectRecognizer{
		userIncludePrefix:   c.userIncludePrefix,
		userIncludeSuffix:   c.userIncludeSuffix,
		groupsIncludePrefix: c.groupsIncludePrefix,
		groupsExcludeSuffix: c.groupsExcludeSuffix,
		workforcePoolName:   c.workforcePoolName,
	}

	inputRBs := rbacv1.RoleBindingList{}
	builder := resource.NewLocalBuilder()
	_ = builder.
		WithScheme(scheme.Scheme, scheme.Scheme.PrioritizedVersionsAllGroups()...).
		DefaultNamespace().
		FilenameParam(
			false,
			&resource.FilenameOptions{Filenames: []string{"-"}}, // Read from stdin
		).
		Do().
		Visit(func(info *resource.Info, _ error) error {
			rbList, ok := info.Object.(*rbacv1.RoleBindingList)
			if !ok {
				return fmt.Errorf("%v is not a RoleBindingList", info.Object)
			}
			inputRBs = *rbList
			return nil
		})

	rewrittenRBs := &rbacv1.RoleBindingList{}
	for _, rbIn := range inputRBs.Items {
		rbOut := rbIn.DeepCopy()

		// Blank out some fields that we don't want to copy over.
		delete(rbOut.ObjectMeta.Annotations, "kubectl.kubernetes.io/last-applied-configuration")
		rbOut.ObjectMeta.CreationTimestamp.Reset()
		rbOut.ObjectMeta.ManagedFields = []metav1.ManagedFieldsEntry{}
		rbOut.ObjectMeta.ResourceVersion = ""
		rbOut.ObjectMeta.UID = types.UID("")

		// Give the object a new name.
		rbOut.ObjectMeta.Name = rbIn.ObjectMeta.Name + "-wfidf"

		rbOut.Subjects = []rbacv1.Subject{}
		for _, sub := range rbIn.Subjects {
			rbOut.Subjects = append(rbOut.Subjects, rec.MigrateSubject(sub))
		}

		rewrittenRBs.Items = append(rewrittenRBs.Items, *rbOut)
	}

	printr := printers.NewTypeSetter(scheme.Scheme).ToPrinter(&printers.YAMLPrinter{})
	if err := printr.PrintObj(rewrittenRBs, os.Stdout); err != nil {
		log.Printf("Error while printing rewritten RoleBindings: %v", err)
		return subcommands.ExitFailure
	}

	return subcommands.ExitSuccess
}

type subjectRecognizer struct {
	userIncludePrefix   string
	userIncludeSuffix   string
	groupsIncludePrefix string
	groupsExcludeSuffix string

	workforcePoolName string
}

func (r *subjectRecognizer) GetFederatedUser(sub rbacv1.Subject) (string, bool) {
	if sub.APIGroup != "rbac.authorization.k8s.io" {
		return "", false
	}
	if sub.Kind != "User" {
		return "", false
	}
	if strings.HasPrefix(sub.Name, "system:") {
		return "", false
	}
	if !strings.HasPrefix(sub.Name, r.userIncludePrefix) {
		return "", false
	}
	if !strings.HasSuffix(sub.Name, r.userIncludeSuffix) {
		return "", false
	}
	return strings.TrimPrefix(sub.Name, r.userIncludePrefix), true
}

func (r *subjectRecognizer) GetFederatedGroup(sub rbacv1.Subject) (string, bool) {
	if sub.APIGroup != "rbac.authorization.k8s.io" {
		return "", false
	}
	if sub.Kind != "Group" {
		return "", false
	}
	if strings.HasPrefix(sub.Name, "system:") {
		return "", false
	}
	if !strings.HasPrefix(sub.Name, r.groupsIncludePrefix) {
		return "", false
	}
	if strings.HasSuffix(sub.Name, r.groupsExcludeSuffix) {
		return "", false
	}
	return strings.TrimPrefix(sub.Name, r.groupsIncludePrefix), true
}

func (r *subjectRecognizer) MigrateSubject(subIn rbacv1.Subject) rbacv1.Subject {
	if name, ok := r.GetFederatedUser(subIn); ok {
		return rbacv1.Subject{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "User",
			Name:     fmt.Sprintf("principal://iam.googleapis.com/locations/global/workforcePools/%s/subject/%s", r.workforcePoolName, name),
		}
	} else if name, ok := r.GetFederatedGroup(subIn); ok {
		return rbacv1.Subject{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Group",
			Name:     fmt.Sprintf("principalSet://iam.googleapis.com/locations/global/workforcePools/%s/group/%s", r.workforcePoolName, name),
		}
	} else {
		// Non-federated subjects are copied over as-is.
		return subIn
	}
}
