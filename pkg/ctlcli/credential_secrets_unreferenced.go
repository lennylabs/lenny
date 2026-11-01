// SPDX-License-Identifier: MIT

package ctlcli

import (
	"context"
	"fmt"
	"io"
	"net/url"
	"slices"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/metadata"
	ctrl "sigs.k8s.io/controller-runtime"

	"github.com/lennylabs/lenny/pkg/credential"
	"github.com/lennylabs/lenny/pkg/ctl"
)

// secretListPageLimit is the page size of the Secret metadata list.
const secretListPageLimit = 500

// secretsResource is the Kubernetes resource the metadata client lists.
var secretsResource = schema.GroupVersionResource{Version: "v1", Resource: "secrets"}

// secretMetadataLister lists one page of Secret metadata in one namespace.
// The metadata client satisfies it, and the response carries no Secret
// values, so none enter this process.
type secretMetadataLister interface {
	List(ctx context.Context, opts metav1.ListOptions) (*metav1.PartialObjectMetadataList, error)
}

// unreferencedSecretLister implements `lenny-ctl admin credential-pools
// list-unreferenced-secrets`. newLister is injected so a test replaces the
// Kubernetes client.
type unreferencedSecretLister struct {
	newLister func(namespace string) (secretMetadataLister, error)
}

// tenantIDWire is one item of the GET /v1/admin/tenants paginated collection.
type tenantIDWire struct {
	ID string `json:"id"`
}

// credentialPoolRefsWire is one item of the GET /v1/admin/credential-pools
// paginated collection, reduced to the credential secretRefs.
type credentialPoolRefsWire struct {
	Name        string `json:"name"`
	Credentials []struct {
		ID        string `json:"id"`
		SecretRef string `json:"secretRef"`
	} `json:"credentials"`
}

// cmdListUnreferencedSecrets runs the listing against the current kubeconfig
// context.
//
// spec: §24.5
func cmdListUnreferencedSecrets(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	cmd := unreferencedSecretLister{newLister: kubeSecretMetadata}
	return cmd.run(ctx, c, args, stdout, stderr)
}

// run collects every name before it prints any, so a failure leaves stdout
// empty: a partial list would present referenced Secrets as unreferenced.
//
// spec: §24.5
func (l unreferencedSecretLister) run(ctx context.Context, c *ctl.Client, args []string, stdout, stderr io.Writer) int {
	namespace := flagValue(args, "--namespace")
	if namespace == "" {
		namespace = credential.DefaultNamespace
	}
	names, err := l.collect(ctx, c, namespace)
	if err != nil {
		fmt.Fprintln(stderr, "lenny-ctl: list-unreferenced-secrets:", err)
		return 1
	}
	for _, name := range names {
		fmt.Fprintln(stdout, name)
	}
	return 0
}

// collect returns the sorted names of the Secrets in namespace that lack the
// reserved prefix and that no credential pool of any tenant references.
func (l unreferencedSecretLister) collect(ctx context.Context, c *ctl.Client, namespace string) ([]string, error) {
	referenced, err := referencedSecretNames(ctx, c, namespace)
	if err != nil {
		return nil, err
	}
	lister, err := l.newLister(namespace)
	if err != nil {
		return nil, err
	}
	names, err := listSecretNames(ctx, lister)
	if err != nil {
		return nil, fmt.Errorf("list Secrets in namespace %s: %w", namespace, err)
	}
	names = slices.DeleteFunc(names, func(name string) bool {
		_, isReferenced := referenced[name]
		return isReferenced || credential.IsManaged(name)
	})
	slices.Sort(names)
	return names, nil
}

// referencedSecretNames resolves every secretRef of every live pool of every
// tenant to its Secret name. A soft-deleted pool holds no reference, so the
// requests omit includeDeleted.
func referencedSecretNames(ctx context.Context, c *ctl.Client, namespace string) (map[string]struct{}, error) {
	tenants, err := listAllAdmin[tenantIDWire](ctx, c, "/v1/admin/tenants")
	if err != nil {
		return nil, fmt.Errorf("list tenants: %w", err)
	}
	referenced := map[string]struct{}{}
	for _, tenant := range tenants {
		pools, err := listAllAdmin[credentialPoolRefsWire](ctx, c,
			"/v1/admin/credential-pools?tenantId="+url.QueryEscape(tenant.ID))
		if err != nil {
			return nil, fmt.Errorf("list credential pools of tenant %s: %w", tenant.ID, err)
		}
		if err := addPoolRefs(referenced, tenant.ID, pools, namespace); err != nil {
			return nil, err
		}
	}
	return referenced, nil
}

// addPoolRefs adds the Secret name of each non-empty secretRef to referenced.
// A ref outside namespace fails the call: the listing cannot tell whether the
// wrong namespace was named or the stored ref is out of place.
func addPoolRefs(referenced map[string]struct{}, tenant string, pools []credentialPoolRefsWire, namespace string) error {
	for _, pool := range pools {
		for _, entry := range pool.Credentials {
			if entry.SecretRef == "" {
				continue
			}
			name, err := credential.ParseSecretRef(entry.SecretRef, namespace)
			if err != nil {
				return fmt.Errorf("tenant %s pool %s credential %s: %w", tenant, pool.Name, entry.ID, err)
			}
			referenced[name] = struct{}{}
		}
	}
	return nil
}

// listSecretNames follows the continue token through every page.
func listSecretNames(ctx context.Context, lister secretMetadataLister) ([]string, error) {
	var names []string
	token := ""
	for {
		page, err := lister.List(ctx, metav1.ListOptions{Limit: secretListPageLimit, Continue: token})
		if err != nil {
			return nil, err
		}
		for i := range page.Items {
			names = append(names, page.Items[i].Name)
		}
		token = page.Continue
		if token == "" {
			return names, nil
		}
	}
}

// kubeSecretMetadata builds the metadata client of namespace from the current
// kubeconfig context.
func kubeSecretMetadata(namespace string) (secretMetadataLister, error) {
	cfg, err := ctrl.GetConfig()
	if err != nil {
		return nil, fmt.Errorf("load the kubeconfig context: %w", err)
	}
	client, err := metadata.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build the Kubernetes metadata client: %w", err)
	}
	return client.Resource(secretsResource).Namespace(namespace), nil
}
