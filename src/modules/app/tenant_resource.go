package app

import (
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

type tenantResource struct {
	dynamic.ResourceInterface
	scope    Scope
	project  string
	resource schema.GroupVersionResource
	cache    *ResourceClient
}

func (s Scope) Resource(client dynamic.Interface, resource schema.GroupVersionResource, project string) dynamic.ResourceInterface {
	resourceClient, _ := client.(*ResourceClient)
	return &tenantResource{ResourceInterface: client.Resource(resource).Namespace(s.ResourceNamespace(project)), scope: s, project: project, resource: resource, cache: resourceClient}
}

func (r *tenantResource) Get(ctx context.Context, name string, options metav1.GetOptions, subresources ...string) (*unstructured.Unstructured, error) {
	object, err := r.ResourceInterface.Get(ctx, name, options, subresources...)
	if err != nil {
		return nil, err
	}
	if !r.scope.Owns(object, r.project) {
		return nil, apierrors.NewNotFound(r.resource.GroupResource(), name)
	}
	return object, nil
}

func (r *tenantResource) List(ctx context.Context, options metav1.ListOptions) (*unstructured.UnstructuredList, error) {
	if r.cache != nil && options.LabelSelector == "" && options.FieldSelector == "" && options.Limit == 0 && options.Continue == "" && options.ResourceVersion == "" {
		return r.cache.projectList(ctx, r.resource, r.scope.ResourceNamespace(r.project), r.project)
	}
	objects, err := r.ResourceInterface.List(ctx, options)
	if err != nil {
		return nil, err
	}
	items := make([]unstructured.Unstructured, 0, len(objects.Items))
	for _, object := range objects.Items {
		if r.scope.Owns(&object, r.project) {
			items = append(items, object)
		}
	}
	objects.Items = items
	return objects, nil
}

func (r *tenantResource) Create(ctx context.Context, object *unstructured.Unstructured, options metav1.CreateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	object = object.DeepCopy()
	object.SetNamespace(r.scope.ResourceNamespace(r.project))
	if err := unstructured.SetNestedField(object.Object, r.project, "spec", "project", "name"); err != nil {
		return nil, err
	}
	return r.ResourceInterface.Create(ctx, object, options, subresources...)
}

func (r *tenantResource) Update(ctx context.Context, object *unstructured.Unstructured, options metav1.UpdateOptions, subresources ...string) (*unstructured.Unstructured, error) {
	if _, err := r.Get(ctx, object.GetName(), metav1.GetOptions{}); err != nil {
		return nil, err
	}
	if !r.scope.Owns(object, r.project) {
		return nil, apierrors.NewBadRequest("spec.project is immutable")
	}
	return r.ResourceInterface.Update(ctx, object, options, subresources...)
}

func (r *tenantResource) Delete(ctx context.Context, name string, options metav1.DeleteOptions, subresources ...string) error {
	object, err := r.Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if options.Preconditions == nil {
		options.Preconditions = &metav1.Preconditions{}
	}
	if options.Preconditions.UID == nil && object.GetUID() != "" {
		uid := object.GetUID()
		options.Preconditions.UID = &uid
	}
	return r.ResourceInterface.Delete(ctx, name, options, subresources...)
}
