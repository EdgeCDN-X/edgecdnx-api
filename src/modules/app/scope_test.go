package app

import (
	"context"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/tools/cache"
)

func TestScopeProjectIndex(t *testing.T) {
	indexer := cache.NewIndexer(cache.MetaNamespaceKeyFunc, cache.Indexers{ProjectIndex: ProjectIndexFunc})
	for _, placement := range []struct{ namespace, project string }{{"tenant", "tenant"}, {"shared", "tenant"}, {"shared", "other"}, {"outside", "tenant"}} {
		resource := &unstructured.Unstructured{Object: map[string]interface{}{
			"metadata": map[string]interface{}{"name": placement.project, "namespace": placement.namespace, "labels": map[string]interface{}{"edgecdnx.com/project-name": "tenant"}},
			"spec":     map[string]interface{}{"project": map[string]interface{}{"name": placement.project}},
		}}
		if err := indexer.Add(resource); err != nil {
			t.Fatal(err)
		}
	}
	for _, scope := range []Scope{{Namespaced: true}, {Namespace: "shared"}} {
		objects, err := scope.ProjectObjects(indexer, "tenant")
		if err != nil || len(objects) != 1 {
			t.Fatalf("scope %+v returned %d resources: %v", scope, len(objects), err)
		}
		if !scope.Owns(objects[0].(*unstructured.Unstructured), "tenant") {
			t.Fatal("indexed resource did not match tenant ownership")
		}
	}
	if err := (Scope{}).Validate(); err == nil {
		t.Fatal("shared scope requires a namespace")
	}
}

func TestTenantResource(t *testing.T) {
	gvr := schema.GroupVersionResource{Group: "infrastructure.edgecdnx.com", Version: "v1alpha1", Resource: "services"}
	foreign := &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "infrastructure.edgecdnx.com/v1alpha1", "kind": "Service",
		"metadata": map[string]interface{}{"name": "foreign", "namespace": "shared", "labels": map[string]interface{}{"project": "tenant"}},
		"spec":     map[string]interface{}{"project": map[string]interface{}{"name": "other"}},
	}}
	client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ServiceList"}, foreign)
	resource := (Scope{Namespace: "shared"}).Resource(client, gvr, "tenant")
	if _, err := resource.Get(context.Background(), "foreign", metav1.GetOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("foreign resource must be hidden: %v", err)
	}
	if err := resource.Delete(context.Background(), "foreign", metav1.DeleteOptions{}); !apierrors.IsNotFound(err) {
		t.Fatalf("foreign resource must not be deleted: %v", err)
	}
	objects, err := resource.List(context.Background(), metav1.ListOptions{})
	if err != nil || len(objects.Items) != 0 {
		t.Fatalf("foreign resources must not be listed: %v", err)
	}
	owned := foreign.DeepCopy()
	owned.SetName("owned")
	created, err := resource.Create(context.Background(), owned, metav1.CreateOptions{})
	if err != nil || !(Scope{Namespace: "shared"}).Owns(created, "tenant") {
		t.Fatalf("creation must stamp tenant ownership: %v", err)
	}
}

func TestResourceInformerPlacement(t *testing.T) {
	for _, scope := range []Scope{{Namespaced: true}, {Namespace: "shared"}} {
		t.Run(scope.ResourceNamespace("tenant"), func(t *testing.T) {
			gvr := schema.GroupVersionResource{Group: "infrastructure.edgecdnx.com", Version: "v1alpha1", Resource: "services"}
			objects := make([]runtime.Object, 0, 2)
			for _, project := range []string{"tenant", "other"} {
				objects = append(objects, &unstructured.Unstructured{Object: map[string]interface{}{
					"apiVersion": "infrastructure.edgecdnx.com/v1alpha1", "kind": "Service",
					"metadata": map[string]interface{}{"name": project, "namespace": scope.ResourceNamespace(project)},
					"spec":     map[string]interface{}{"project": map[string]interface{}{"name": project}},
				}})
			}
			client := fake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{gvr: "ServiceList"}, objects...)
			cached, err := NewResourceClient(client, scope, gvr)
			if err != nil {
				t.Fatal(err)
			}
			defer CloseResourceClient(cached)
			client.ClearActions()
			resource := scope.Resource(cached, gvr, "tenant")
			listed, err := resource.List(context.Background(), metav1.ListOptions{})
			if err != nil || len(listed.Items) != 1 || listed.Items[0].GetName() != "tenant" {
				t.Fatalf("indexed list failed: %v, %v", listed, err)
			}
			for _, action := range client.Actions() {
				if action.GetVerb() == "list" {
					t.Fatal("tenant list bypassed the informer index")
				}
			}
			listed.Items[0].SetName("mutated")
			listed, err = resource.List(context.Background(), metav1.ListOptions{})
			if err != nil || listed.Items[0].GetName() != "tenant" {
				t.Fatal("response mutation changed the informer cache")
			}
		})
	}
}
