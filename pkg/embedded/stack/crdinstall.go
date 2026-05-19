// SPDX-License-Identifier: MIT

package stack

import (
	"context"
	"fmt"
	"path/filepath"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	apiextensionsclient "k8s.io/apiextensions-apiserver/pkg/client/clientset/clientset"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"

	embeddedcrds "github.com/lennylabs/lenny/pkg/embedded/crds"
)

// installCRDs applies every lenny.dev CustomResourceDefinition to the
// embedded cluster. §17.4: Embedded Mode uses the production CRDs. The
// manifests are embedded in the binary, so this needs no checkout of
// the Helm chart. Each CRD is created when absent and updated in place
// when it already exists, which keeps lenny up idempotent.
func installCRDs(ctx context.Context, kubeconfigPath string) error {
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfigPath)
	if err != nil {
		return fmt.Errorf("embedded crd: load kubeconfig %s: %w", kubeconfigPath, err)
	}
	client, err := apiextensionsclient.NewForConfig(cfg)
	if err != nil {
		return fmt.Errorf("embedded crd: build apiextensions client: %w", err)
	}
	entries, err := embeddedcrds.FS.ReadDir(".")
	if err != nil {
		return fmt.Errorf("embedded crd: list embedded manifests: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		raw, err := embeddedcrds.FS.ReadFile(e.Name())
		if err != nil {
			return fmt.Errorf("embedded crd: read %s: %w", e.Name(), err)
		}
		var crd apiextensionsv1.CustomResourceDefinition
		if err := yaml.Unmarshal(raw, &crd); err != nil {
			return fmt.Errorf("embedded crd: parse %s: %w", e.Name(), err)
		}
		if err := applyCRD(ctx, client, &crd); err != nil {
			return err
		}
	}
	return nil
}

// applyCRD creates crd, or updates the existing definition's spec in
// place when a CRD of that name is already registered.
func applyCRD(ctx context.Context, client apiextensionsclient.Interface, crd *apiextensionsv1.CustomResourceDefinition) error {
	api := client.ApiextensionsV1().CustomResourceDefinitions()
	existing, err := api.Get(ctx, crd.Name, metav1.GetOptions{})
	if apierrors.IsNotFound(err) {
		if _, err := api.Create(ctx, crd, metav1.CreateOptions{}); err != nil {
			return fmt.Errorf("embedded crd: create %s: %w", crd.Name, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("embedded crd: get %s: %w", crd.Name, err)
	}
	existing.Spec = crd.Spec
	if _, err := api.Update(ctx, existing, metav1.UpdateOptions{}); err != nil {
		return fmt.Errorf("embedded crd: update %s: %w", crd.Name, err)
	}
	return nil
}
